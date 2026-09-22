#!/usr/bin/env python3
"""Low-cost, secret-safe smoke checks for DZ23 OpenAI-compatible providers."""

import json
import os
import sys
import urllib.error
import urllib.request


PROVIDERS = [
    ("openrouter", "OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", "openrouter/free"),
    ("deepseek", "DEEPSEEK_API_KEY", "https://api.deepseek.com/v1", "deepseek-chat"),
    ("groq", "GROQ_API_KEY", "https://api.groq.com/openai/v1", "llama-3.1-8b-instant"),
    ("together", "TOGETHER_API_KEY", "https://api.together.xyz/v1", "meta-llama/Llama-3.3-70B-Instruct-Turbo-Free"),
    ("fireworks", "FIREWORKS_API_KEY", "https://api.fireworks.ai/inference/v1", "accounts/fireworks/models/llama-v3p1-8b-instruct"),
    ("cerebras", "CEREBRAS_API_KEY", "https://api.cerebras.ai/v1", "llama3.1-8b"),
    ("mistral", "MISTRAL_API_KEY", "https://api.mistral.ai/v1", "mistral-small-latest"),
    ("xai", "XAI_API_KEY", "https://api.x.ai/v1", "grok-3-mini"),
    ("sambanova", "SAMBANOVA_API_KEY", "https://api.sambanova.ai/v1", "Meta-Llama-3.1-8B-Instruct"),
    ("nvidia", "NVIDIA_API_KEY", "https://integrate.api.nvidia.com/v1", "meta/llama-3.1-8b-instruct"),
    ("novita", "NOVITA_API_KEY", "https://api.novita.ai/openai/v1", "meta-llama/llama-3.1-8b-instruct"),
    ("upstage", "UPSTAGE_API_KEY", "https://api.upstage.ai/v1/solar", "solar-pro2"),
    ("ollama-cloud", "OLLAMA_API_KEY", "https://ollama.com/v1", "gpt-oss:20b"),
    ("hyperbolic", "HYPERBOLIC_API_KEY", "https://api.hyperbolic.xyz/v1", "meta-llama/Meta-Llama-3.1-8B-Instruct"),
    ("alibaba", "ALIBABA_API_KEY", os.getenv("ALIBABA_BASE_URL", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"), "qwen-turbo"),
    ("gemini", "GEMINI_API_KEY", "https://generativelanguage.googleapis.com/v1beta/openai", os.getenv("GEMINI_MODEL", "gemini-2.5-flash")),
    ("huggingface", "HUGGINGFACE_TOKEN", "https://router.huggingface.co/v1", os.getenv("HUGGINGFACE_MODEL", "deepseek-ai/DeepSeek-R1:fastest")),
]


def request(url, key, payload=None):
    headers = {"Authorization": f"Bearer {key}", "Accept": "application/json"}
    data = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, headers=headers, method="POST" if data else "GET")
    with urllib.request.urlopen(req, timeout=20) as response:
        return response.status, json.loads(response.read(1_000_000))


def select_model(base_url, key, fallback):
    try:
        _, body = request(base_url.rstrip("/") + "/models", key)
        ids = [item.get("id") for item in body.get("data", []) if isinstance(item, dict) and item.get("id")]
        if fallback in ids or not ids:
            return fallback
        candidates = [model for model in ids if not any(word in model.lower() for word in ("embed", "image", "audio", "moderation", "rerank"))]
        return candidates[0] if candidates else fallback
    except Exception:
        return fallback


def classify(exc):
    if isinstance(exc, urllib.error.HTTPError):
        return {401: "INVALID_CREDENTIAL", 403: "FORBIDDEN", 404: "INVALID_MODEL", 429: "RATE_LIMITED"}.get(exc.code, f"HTTP_{exc.code}")
    if isinstance(exc, urllib.error.URLError):
        return "NETWORK_ERROR"
    return "ERROR"


def main():
    failed = 0
    tested = 0
    print("provider\tstatus\thttp")
    for name, env_name, base_url, fallback in PROVIDERS:
        key = os.getenv(env_name, "").strip()
        if not key:
            print(f"{name}\tNOT_CONFIGURED\t-")
            continue
        tested += 1
        model = select_model(base_url, key, fallback)
        payload = {"model": model, "messages": [{"role": "user", "content": "Reply only OK"}], "max_tokens": 3, "temperature": 0}
        try:
            status, body = request(base_url.rstrip("/") + "/chat/completions", key, payload)
            choices = body.get("choices", []) if isinstance(body, dict) else []
            result = "PASS" if status == 200 and choices else "INVALID_RESPONSE"
            failed += result != "PASS"
            print(f"{name}\t{result}\t{status}")
        except Exception as exc:
            failed += 1
            http = exc.code if isinstance(exc, urllib.error.HTTPError) else "-"
            print(f"{name}\t{classify(exc)}\t{http}")
    print(f"summary\ttested={tested}; failed={failed}\t-")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
