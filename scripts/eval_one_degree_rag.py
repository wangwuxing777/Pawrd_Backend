#!/usr/bin/env python3
import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_QUESTIONS = ROOT / "assets" / "rag" / "one_degree_eval_questions.json"
DEFAULT_JUDGE_PROMPT = ROOT / "assets" / "rag" / "one_degree_judge_prompt.md"
DEFAULT_OUTPUT = ROOT / "artifacts" / "eval" / "one_degree_eval_results.json"


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        os.environ.setdefault(key, value)


def derive_base_url(endpoint: str) -> str:
    endpoint = (endpoint or "").strip().rstrip("/")
    for suffix in ("/chat/completions", "/completions"):
        if endpoint.endswith(suffix):
            return endpoint[: -len(suffix)]
    return endpoint


def post_json(url: str, payload: Dict[str, Any], headers: Optional[Dict[str, str]] = None, timeout: int = 120) -> Dict[str, Any]:
    body = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(url, data=body, method="POST")
    request.add_header("Content-Type", "application/json")
    for key, value in (headers or {}).items():
        request.add_header(key, value)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def strip_code_fence(text: str) -> str:
    text = text.strip()
    match = re.search(r"```(?:json)?\s*(\{.*\})\s*```", text, re.DOTALL)
    if match:
        return match.group(1).strip()
    return text


def extract_first_json_object(text: str) -> str:
    text = strip_code_fence(text)
    start = text.find("{")
    if start == -1:
        raise ValueError("no JSON object found")
    depth = 0
    for idx in range(start, len(text)):
        char = text[idx]
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return text[start : idx + 1]
    raise ValueError("unterminated JSON object")


def load_json(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def weighted_score(scores: Dict[str, Any]) -> float:
    dims = {
        "factual_accuracy": 0.50,
        "exclusions_conditions": 0.30,
        "boundary_control": 0.20,
    }
    total = 0.0
    for key, weight in dims.items():
        value = scores.get(key)
        if not isinstance(value, (int, float)):
            value = 1
        total += max(1, min(5, value)) * weight
    return round((total / 5.0) * 100.0, 2)


def verdict_for(score: float, critical_error: bool) -> str:
    if critical_error:
        return "fail"
    if score >= 85:
        return "pass"
    if score >= 70:
        return "borderline"
    return "fail"


def ask_local_rag(base_url: str, provider: str, question: str) -> Dict[str, Any]:
    url = base_url.rstrip("/") + "/api/chat"
    payload = {"query": question, "provider": provider}
    return post_json(url, payload, timeout=180)


def judge_answer(
    judge_cfg: Dict[str, Any],
    prompt_template: str,
    case: Dict[str, Any],
    rag_result: Dict[str, Any],
) -> Tuple[Optional[Dict[str, Any]], Optional[str]]:
    if not judge_cfg["enabled"]:
        return None, None

    user_payload = {
        "question": case["question"],
        "question_type": case.get("category"),
        "expected_provider": case.get("expected_provider"),
        "gold_facts": case.get("gold_facts", []),
        "source_refs": case.get("source_refs", []),
        "model_answer": rag_result.get("answer", ""),
        "returned_provider": rag_result.get("active_provider"),
        "returned_sources": rag_result.get("sources", []),
    }
    user_prompt = (
        prompt_template.strip()
        + "\n\nEvaluate this case:\n"
        + json.dumps(user_payload, ensure_ascii=False, indent=2)
    )

    payload = {
        "model": judge_cfg["model"],
        "messages": [
            {"role": "system", "content": "You are a strict evaluator. Return JSON only."},
            {"role": "user", "content": user_prompt},
        ],
        "temperature": 0,
        "max_tokens": 1200,
    }
    headers = {"Authorization": f"Bearer {judge_cfg['api_key']}"}
    response = post_json(judge_cfg["base_url"].rstrip("/") + "/chat/completions", payload, headers=headers, timeout=180)
    raw_content = extract_chat_content(response)
    parsed = json.loads(extract_first_json_object(raw_content))
    return parsed, raw_content


def extract_chat_content(response: Dict[str, Any]) -> str:
    choices = response.get("choices") or []
    if not choices:
        raise ValueError("judge response missing choices")
    message = (choices[0] or {}).get("message") or {}
    content = message.get("content", "")
    if isinstance(content, str):
        if content.strip():
            return content
    if isinstance(content, list):
        parts = []
        for item in content:
            if isinstance(item, dict) and isinstance(item.get("text"), str):
                parts.append(item["text"])
        joined = "\n".join(parts)
        if joined.strip():
            return joined
    reasoning = message.get("reasoning_content", "")
    if isinstance(reasoning, str):
        return reasoning
    if isinstance(reasoning, list):
        parts = []
        for item in reasoning:
            if isinstance(item, dict) and isinstance(item.get("text"), str):
                parts.append(item["text"])
        return "\n".join(parts)
    return str(content)


def build_judge_config() -> Dict[str, Any]:
    base = (
        os.getenv("RAG_JUDGE_BASE_URL")
        or os.getenv("HK_INSURANCE_RAG_LLM_BASE_URL")
        or derive_base_url(os.getenv("REPORT_AGENT_1_ENDPOINT", ""))
    )
    api_key = (
        os.getenv("RAG_JUDGE_API_KEY")
        or os.getenv("HK_INSURANCE_RAG_LLM_API_KEY")
        or os.getenv("REPORT_AGENT_1_API_KEY")
    )
    model = (
        os.getenv("RAG_JUDGE_MODEL")
        or os.getenv("HK_INSURANCE_RAG_LLM_MODEL")
        or "Qwen/Qwen2.5-7B-Instruct"
    )
    enabled = bool(base and api_key and model)
    return {
        "enabled": enabled,
        "base_url": derive_base_url(base),
        "api_key": api_key,
        "model": model,
    }


def aggregate(cases: List[Dict[str, Any]]) -> Dict[str, Any]:
    judged = [item for item in cases if item.get("judge")]
    summary = {
        "total_cases": len(cases),
        "judged_cases": len(judged),
        "pass": 0,
        "borderline": 0,
        "fail": 0,
        "critical_error_cases": [],
        "average_weighted_score": None,
        "average_scores": {},
    }
    if not judged:
        return summary

    total_weighted = 0.0
    dim_totals = {
        "factual_accuracy": 0.0,
        "exclusions_conditions": 0.0,
        "boundary_control": 0.0,
    }
    for item in judged:
        summary[item["judge"]["verdict"]] += 1
        total_weighted += item["judge"]["weighted_score"]
        for key in dim_totals:
            value = item["judge"]["scores"].get(key, 1)
            if not isinstance(value, (int, float)):
                value = 1
            dim_totals[key] += value
        if item["judge"].get("critical_error"):
            summary["critical_error_cases"].append(item["id"])
    summary["average_weighted_score"] = round(total_weighted / len(judged), 2)
    summary["average_scores"] = {k: round(v / len(judged), 2) for k, v in dim_totals.items()}
    return summary


def main() -> int:
    parser = argparse.ArgumentParser(description="Evaluate local OneDegree RAG answers with LLM-as-Judge.")
    parser.add_argument("--base-url", default="http://127.0.0.1:8000", help="Local backend base URL")
    parser.add_argument("--provider", default="one_degree", help="Provider id to force on /api/chat")
    parser.add_argument("--questions", default=str(DEFAULT_QUESTIONS), help="Questions JSON path")
    parser.add_argument("--judge-prompt", default=str(DEFAULT_JUDGE_PROMPT), help="Judge prompt markdown path")
    parser.add_argument("--out", default=str(DEFAULT_OUTPUT), help="Output JSON path")
    parser.add_argument("--limit", type=int, default=0, help="Only run first N cases")
    parser.add_argument("--no-judge", action="store_true", help="Skip judge call and only collect answers")
    args = parser.parse_args()

    load_dotenv(ROOT / ".env")
    load_dotenv(ROOT / ".env.local")

    questions = load_json(Path(args.questions))
    if args.limit > 0:
        questions = questions[: args.limit]
    prompt_template = Path(args.judge_prompt).read_text(encoding="utf-8")
    judge_cfg = build_judge_config()
    if args.no_judge:
        judge_cfg["enabled"] = False

    results = []
    for index, case in enumerate(questions, start=1):
        print(f"[{index}/{len(questions)}] Asking: {case['id']} - {case['question']}")
        rag_result = ask_local_rag(args.base_url, args.provider, case["question"])
        record = {
            "id": case["id"],
            "category": case.get("category"),
            "question": case["question"],
            "expected_provider": case.get("expected_provider"),
            "gold_facts": case.get("gold_facts", []),
            "source_refs": case.get("source_refs", []),
            "rag_response": rag_result,
        }

        if judge_cfg["enabled"]:
            try:
                judged, raw_judge = judge_answer(judge_cfg, prompt_template, case, rag_result)
                scores = judged.get("scores", {}) if judged else {}
                weight = weighted_score(scores)
                critical_error = bool((judged or {}).get("critical_error"))
                record["judge"] = {
                    **(judged or {}),
                    "weighted_score": weight,
                    "verdict": verdict_for(weight, critical_error),
                }
                record["raw_judge_response"] = raw_judge
            except Exception as exc:
                record["judge_error"] = str(exc)
        results.append(record)
        time.sleep(0.2)

    output = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "provider": args.provider,
        "base_url": args.base_url,
        "judge": {
            "enabled": judge_cfg["enabled"],
            "base_url": judge_cfg.get("base_url"),
            "model": judge_cfg.get("model"),
        },
        "summary": aggregate(results),
        "cases": results,
    }

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(output, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"\nSaved results to: {out_path}")
    print(json.dumps(output["summary"], ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except urllib.error.URLError as exc:
        print(f"Network/connection error: {exc}", file=sys.stderr)
        raise SystemExit(2)
