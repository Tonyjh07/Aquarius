# -*- coding: utf-8 -*-
# 一次性 fixture 生成脚本（勿在测试中执行）：
# 构造一个结构与 DeepSeek 一致、但词表极小的 HF tokenizer.json（256 个字节级
# 单字 token + 少量合并链 + 2 个 added token），供 Go 侧流水线（切分/ByteLevel/
# BPE 合并/added token）做全离线自洽测试；期望计数由官方 tokenizers 计算。
import io
import json
import sys

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")
from tokenizers import Tokenizer  # noqa: E402

OUT_JSON = r"internal/adapter/llm/tokenizer/testdata/fixture_tokenizer.json"
OUT_VEC = r"internal/adapter/llm/tokenizer/testdata/fixture_vectors.json"


def byte_chars():
    """GPT-2 bytes_to_unicode：可打印字节保持，其余映射到 256+n。"""
    bs = (
        list(range(ord("!"), ord("~") + 1))
        + list(range(ord("¡"), ord("¬") + 1))
        + list(range(ord("®"), ord("ÿ") + 1))
    )
    cs = bs[:]
    n = 0
    for b in range(256):
        if b not in bs:
            bs.append(b)
            cs.append(256 + n)
            n += 1
    return {b: chr(c) for b, c in zip(bs, cs)}


B2U = byte_chars()


def mapped(s):
    """字符串 → 字节级映射串（按 UTF-8 字节）。"""
    return "".join(B2U[b] for b in s.encode("utf-8"))


vocab = {B2U[b]: i for i, b in enumerate(range(256))}  # 0..255：全部单字节 token
merges = []


def add(sym):
    if sym not in vocab:
        vocab[sym] = len(vocab)
    return vocab[sym]


def chain(word):
    """左结合合并链：word（字节映射串）→ 若干 merges + 合并 token。"""
    syms = list(word)
    while len(syms) > 1:
        pair = syms[0] + syms[1]
        merges.append(syms[0] + " " + syms[1])
        add(pair)
        # 每步只合并最左对（与自造 merges 的秩序一致）
        syms = [pair] + syms[2:]
        # 注意：真实 BPE 按秩全局选对；这里词汇唯一、目标串从左到右，等价。
        i = 0
        while i + 1 < len(syms) and False:
            i += 1
    # 上面的极简链只适用于两两顺序……改用标准逐对构建：
    return


def build(word):
    """标准左结合：h e l l o → he → hel → hell → hello。"""
    syms = list(word)
    while len(syms) > 1:
        a, b = syms[0], syms[1]
        merged = a + b
        merges.append(a + " " + b)
        add(merged)
        syms = [merged] + syms[2:]


# 目标词：ASCII 词（带/不带前导空格）、CJK（按 UTF-8 字节映射后合并）。
for w in ["hello", " world", "world", "test", "你", "好", "，", "🙂"]:
    build(mapped(w))

added = [
    {"id": len(vocab), "content": "<s>", "single_word": False, "lstrip": False,
     "rstrip": False, "normalized": False, "special": True},
    {"id": len(vocab) + 1, "content": "</s>", "single_word": False, "lstrip": False,
     "rstrip": False, "normalized": False, "special": True},
]
vocab["<s>"] = added[0]["id"]
vocab["</s>"] = added[1]["id"]

fixture = {
    "version": "1.0",
    "truncation": None,
    "padding": None,
    "added_tokens": added,
    "normalizer": {"type": "Sequence", "normalizers": []},
    "pre_tokenizer": {
        "type": "Sequence",
        "pretokenizers": [
            {"type": "Split", "pattern": {"Regex": r"\p{N}{1,3}"},
             "behavior": "Isolated", "invert": False},
            {"type": "Split", "pattern": {"Regex": "[一-龥]+"},
             "behavior": "Isolated", "invert": False},
            {"type": "Split",
             "pattern": {"Regex": "[A-Za-z]+| ?[\\p{P}\\p{S}]+|\\s+(?!\\S)|\\s+"},
             "behavior": "Isolated", "invert": False},
            {"type": "ByteLevel", "add_prefix_space": False, "trim_offsets": True,
             "use_regex": False},
        ],
    },
    "post_processor": {"type": "ByteLevel", "add_prefix_space": True,
                       "trim_offsets": False, "use_regex": True},
    "decoder": {"type": "ByteLevel", "add_prefix_space": True,
                "trim_offsets": True, "use_regex": True},
    "model": {"type": "BPE", "dropout": None, "unk_token": None,
              "continuing_subword_prefix": None, "end_of_word_suffix": None,
              "fuse_unk": False, "byte_fallback": False,
              "vocab": vocab, "merges": merges},
}

with open(OUT_JSON, "w", encoding="utf-8") as f:
    json.dump(fixture, f, ensure_ascii=False)
    f.write("\n")

SAMPLES = [
    "",
    "hello",
    "hello world",
    "hello world test",
    "你好",
    "hello 你好 world",
    "1234567890",
    "Hello, world!",
    "<s>hello</s>",
    "  spaced  out  ",
    "🙂 emoji",
    "no-merges-for-this xyz",
    "你，好。",
]

tok = Tokenizer.from_file(OUT_JSON)
out = [{"text": s, "count": len(tok.encode(s, add_special_tokens=False).ids)} for s in SAMPLES]
with open(OUT_VEC, "w", encoding="utf-8") as f:
    json.dump(out, f, ensure_ascii=False, indent=1)
    f.write("\n")

# 自检：重载一遍确认文件可解析；打印若干计数供肉眼核对。
print("fixture vocab:", len(vocab), "merges:", len(merges))
for v in out:
    print(repr(v["text"]), "->", v["count"])
