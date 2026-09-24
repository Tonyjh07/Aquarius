# -*- coding: utf-8 -*-
# 一次性 ground-truth 生成脚本（勿在测试中执行）：
# 用 HuggingFace 官方 tokenizers 对 DeepSeek tokenizer.json 编码样例，产出 vectors.json。
import io
import json
import sys

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")
from tokenizers import Tokenizer  # noqa: E402

SRC = __import__("os").environ.get(
    "AQUARIUS_TOKENIZER_JSON",
    r"C:\Users\yjh07\Downloads\deepseek_v4_tokenizer\deepseek_v4_tokenizer\tokenizer.json",
)
OUT = r"internal/adapter/llm/tokenizer/testdata/vectors.json"

SAMPLES = [
    "",
    "Hello!",
    "The quick brown fox jumps over the lazy dog.",
    "你好，世界！这是一个测试。",
    "混合 mixed 中英文 text 12345 测试",
    "1234567890",
    "!@#$%^&*()_+-=[]{}{}|;:',.<>?/`~",
    "   leading and trailing   ",
    "line1\r\nline2\nline3\ttabbed",
    "<|User|>你好<|Assistant|>好的",
    "<｜begin▁of▁sentence｜>system prompt<｜end▁of▁sentence｜>",
    'func main() {\n\tfmt.Println("hello world")\n}',
    "こんにちは、世界です。",
    "안녕하세요 세계",
    "🎉 emoji test 🚀 and more",
    "https://example.com/path?query=1&x=2#frag",
    "aGVsbG8gd29ybGQgdGhpcyBpcyBhIHRlc3Q=",
    "Привет мир",
    "天地玄黃宇宙洪荒",
    "user@example.com  2026-09-24  12:34:56",
    "这是一段较长的中文文本，用来测试连续汉字与标点的分词行为，包含逗号、句号、"
    "以及夹杂的 English words 和数字 42，验证 BPE 合并的稳定性。",
    "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor "
    "incididunt ut labore et dolore magna aliqua.",
    "tab\ttab\ttab newline\n\n\nspaces    between    words",
    "（全角括号）与《书名号》、省略号……感叹号！？",
]

tok = Tokenizer.from_file(SRC)
out = [{"text": s, "count": len(tok.encode(s, add_special_tokens=False).ids)} for s in SAMPLES]
with open(OUT, "w", encoding="utf-8") as f:
    json.dump(out, f, ensure_ascii=False, indent=1)
    f.write("\n")
print("vectors:", len(out))
for v in out[:5]:
    print(repr(v["text"][:32]), "->", v["count"])
