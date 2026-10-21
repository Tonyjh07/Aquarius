package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 提问工具的题量/项量/文案长度上限（D184）。上限写在常量里，声明与校验同源
// （D171）：描述里手写一个数字，改常量时必漂。
const (
	maxAskQuestions        = 4
	maxAskOptions          = 9
	maxAskHeaderChars      = 30
	maxAskLabelChars       = 40
	maxAskQuestionChars    = 500
	maxAskDescriptionChars = 200
)

// askInput 模型的原始入参（与 port.AskRequest 分开：前者是模型面形状，
// 后者是端口契约，中间这一层做校验与裁剪）。
type askInput struct {
	Questions []struct {
		Header   string `json:"header"`
		Question string `json:"question"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		Multiple bool `json:"multiple"`
	} `json:"questions"`
}

// askUser 向用户提问（D184）：Safe（提问没有副作用，不该被权限等级拦住）。
//
// 与确认面（Confirmer）的区别：确认答「准不准做」，这里答「选哪个」——
// 1–4 题、每题 0–9 个选项、可多选，UI 自动追加「自己输入」项。
type askUser struct{ asker port.Asker }

func (t *askUser) Spec() tool.Spec {
	return tool.Spec{
		Name: "ask_user",
		Description: fmt.Sprintf("Ask the user a question when the decision is theirs and guessing would waste the turn: "+
			"which of several options to take, which file or target to use, what the intended scope is. "+
			"Send 1-%d questions at once; each question takes 0-%d options (0 = free-form answer only) and may allow multiple selections. "+
			"The interface always appends its own \"type your own answer\" row, so never add a free-form option yourself. "+
			"Put the recommended option first and end its label with \"(Recommended)\". "+
			"Answers come back in the tool result, aligned with your questions in order. "+
			"Do not use it for permission checks (those are asked separately) and do not use it to confirm what you already know.",
			maxAskQuestions, maxAskOptions),
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"questions": {
					"type": "array",
					"description": "1-%d questions; each is answered independently",
					"minItems": 1,
					"maxItems": %d,
					"items": {
						"type": "object",
						"properties": {
							"header": {"type": "string", "description": "short tab label (max %d characters)"},
							"question": {"type": "string", "description": "the question itself (max %d characters)"},
							"options": {
								"type": "array",
								"description": "0-%d options; leave empty for a free-form answer",
								"maxItems": %d,
								"items": {
									"type": "object",
									"properties": {
										"label": {"type": "string", "description": "option label (max %d characters)"},
										"description": {"type": "string", "description": "one line explaining the option (max %d characters)"}
									},
									"required": ["label"]
								}
							},
							"multiple": {"type": "boolean", "description": "allow selecting more than one option"}
						},
						"required": ["header", "question"]
					}
				}
			},
			"required": ["questions"]
		}`, maxAskQuestions, maxAskQuestions, maxAskHeaderChars, maxAskQuestionChars,
			maxAskOptions, maxAskOptions, maxAskLabelChars, maxAskDescriptionChars)),
		Risk: tool.Safe,
	}
}

// Interactive 不吃默认工具超时（D184）：用户可能要想两分钟。
func (t *askUser) Interactive() bool { return true }

func (t *askUser) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var in askInput
	if err := decodeArgs(call, &in); err != nil {
		return tool.Result{}, err
	}
	req, err := askRequest(in)
	if err != nil {
		return tool.Result{}, err
	}
	if t.asker == nil {
		return tool.Result{}, errors.New("ask_user: asking the user is not available in this build")
	}
	ans, err := t.asker.Ask(ctx, req)
	if err != nil {
		// ctx 取消 = 用户中断本轮（Esc / 停止键）：原样上抛，Runner 按装配级故障
		// 中止 Turn。不能降级成「放弃全部」——那是「你自己看着办」，不是「别继续了」。
		return tool.Result{}, err
	}
	if ans.Dismissed {
		return tool.Result{OK: false, Err: askDismissedText}, nil
	}
	if ans.Away {
		return tool.Result{OK: false, Err: askAwayText}, nil
	}
	return okResult(askAnswerText(req.Questions, ans.Answers)), nil
}

// askRequest 校验并规范化模型给的问题（D184 口径一）。
func askRequest(in askInput) (port.AskRequest, error) {
	if len(in.Questions) == 0 {
		return port.AskRequest{}, errors.New("questions must not be empty; send 1-4 questions")
	}
	if len(in.Questions) > maxAskQuestions {
		return port.AskRequest{}, fmt.Errorf("too many questions: %d (at most %d)", len(in.Questions), maxAskQuestions)
	}
	out := make([]port.AskQuestion, 0, len(in.Questions))
	for i, q := range in.Questions {
		question := clipRunes(strings.TrimSpace(q.Question), maxAskQuestionChars)
		if question == "" {
			return port.AskRequest{}, fmt.Errorf("question %d is empty", i+1)
		}
		header := strings.TrimSpace(q.Header)
		if header == "" {
			// 页签总要有个名字：缺省退化成题号，而不是报错把整次提问废掉。
			header = fmt.Sprintf("Q%d", i+1)
		}
		header = clipRunes(header, maxAskHeaderChars)
		if len(q.Options) > maxAskOptions {
			return port.AskRequest{}, fmt.Errorf("question %d has %d options (at most %d)", i+1, len(q.Options), maxAskOptions)
		}
		opts := make([]port.AskOption, 0, len(q.Options))
		for j, o := range q.Options {
			label := strings.TrimSpace(o.Label)
			if label == "" {
				return port.AskRequest{}, fmt.Errorf("question %d option %d has an empty label", i+1, j+1)
			}
			opts = append(opts, port.AskOption{
				Label:       clipRunes(label, maxAskLabelChars),
				Description: clipRunes(strings.TrimSpace(o.Description), maxAskDescriptionChars),
			})
		}
		out = append(out, port.AskQuestion{
			Header:   header,
			Question: question,
			Options:  opts,
			Multiple: q.Multiple,
		})
	}
	return port.AskRequest{Questions: out}, nil
}

// clipRunes 按字符数截断（超长不报错：题干/标签是模型自由写的，截断比废掉整次提问好）。
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// 模型面文案（D168：英文、集中可门禁）。
const (
	// askDismissedText 用户点了「忽略」= 放弃全部（D184 口径三）。
	askDismissedText = "The user dismissed the questions without answering. " +
		"Proceed with your best judgement and state your assumptions."
	// askAwayText 无人值守：用户不在，没人能答（D184 口径九）。
	askAwayText = "The user is not available to answer (unattended run). " +
		"Proceed with your best judgement, state your assumptions, and do not ask again."
	// askUnanswered 单题未答（多题时用户可能只答了一部分）。
	askUnanswered = "Unanswered"
)

// askAnswerText 把答案拼成模型可读的一行（D184 口径二）：
// `User has answered your questions: "Q1"="A1, A2"; "Q2"="Unanswered".`
func askAnswerText(qs []port.AskQuestion, answers [][]string) string {
	parts := make([]string, 0, len(qs))
	for i, q := range qs {
		var picked []string
		if i < len(answers) {
			for _, a := range answers[i] {
				if s := strings.TrimSpace(a); s != "" {
					picked = append(picked, s)
				}
			}
		}
		val := askUnanswered
		if len(picked) > 0 {
			val = strings.Join(picked, ", ")
		}
		parts = append(parts, fmt.Sprintf("%q=%q", q.Question, val))
	}
	return "User has answered your questions: " + strings.Join(parts, "; ") + "."
}
