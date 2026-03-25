"""Singleton NLP model loader and batch inference."""

import os
from concurrent.futures import ThreadPoolExecutor
from typing import Optional

from transformers import pipeline

TOPICS = ["Economic", "Security", "Healthcare", "Environment", "Elections", "Diplomacy"]

# Max text length fed to transformers (tokens are ~4 chars; 512 tokens ≈ 2048 chars).
_MAX_TEXT_LEN = 2000
# Batch sizes tuned for CPU; reduce if OOM on GPU.
_NER_BATCH = 32
_CLF_BATCH = 8


class NLPModel:
    """Loads dslim/bert-base-NER and facebook/bart-large-mnli as singletons
    and exposes a single analyze_batch() method for the gRPC handler."""

    def __init__(self) -> None:
        print("[nlp] loading NER model (dslim/bert-base-NER)…")
        self._ner = pipeline(
            "ner",
            model="dslim/bert-base-NER",
            aggregation_strategy="simple",
        )

        print("[nlp] loading classifier (facebook/bart-large-mnli)…")
        self._clf = pipeline(
            "zero-shot-classification",
            model="facebook/bart-large-mnli",
        )

        api_key = os.getenv("OPENAI_API_KEY")
        self._openai: Optional[object] = None
        if api_key:
            from openai import OpenAI  # lazy import — only needed when key present
            self._openai = OpenAI(api_key=api_key)
            print("[nlp] OpenAI summarisation enabled")
        else:
            print("[nlp] no OPENAI_API_KEY — summaries will be truncated text")

        print("[nlp] models ready")

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def analyze_batch(self, texts: list[str]) -> list[dict]:
        """Run NER, topic classification, and summarisation for a batch.

        Returns a list of dicts with keys: entities, topic, confidence, summary.
        Order matches the input list.
        """
        cleaned = [t[:_MAX_TEXT_LEN] for t in texts]

        entities_batch = self._run_ner(cleaned)
        clf_batch = self._run_clf(cleaned)
        summaries = self._run_summarise(texts)  # use full text for summaries

        results = []
        for i, text in enumerate(texts):
            clf = clf_batch[i]
            results.append({
                "entities": entities_batch[i],
                "topic": clf["labels"][0],
                "confidence": float(clf["scores"][0]),
                "summary": summaries[i],
            })
        return results

    # ------------------------------------------------------------------
    # Private helpers
    # ------------------------------------------------------------------

    def _run_ner(self, texts: list[str]) -> list[dict[str, list[str]]]:
        """Returns one entity-group dict per text: {"PER": ["Alice", …], …}"""
        raw_batch = self._ner(texts, batch_size=_NER_BATCH)
        # When a single string is passed the pipeline returns a flat list;
        # when a list is passed it returns a list of lists.
        if texts and not isinstance(raw_batch[0], list):
            raw_batch = [raw_batch]

        grouped = []
        for entities in raw_batch:
            groups: dict[str, list[str]] = {}
            for ent in entities:
                g = ent["entity_group"]
                groups.setdefault(g, [])
                word = ent["word"].strip()
                if word and word not in groups[g]:
                    groups[g].append(word)
            grouped.append(groups)
        return grouped

    def _run_clf(self, texts: list[str]) -> list[dict]:
        """Returns zero-shot classification result dicts."""
        results = self._clf(texts, candidate_labels=TOPICS, batch_size=_CLF_BATCH)
        # Normalise: single text → wrap in list
        if isinstance(results, dict):
            results = [results]
        return results

    def _run_summarise(self, texts: list[str]) -> list[str]:
        if self._openai:
            return self._summarise_openai(texts)
        # Fallback: first sentence or first 200 chars
        return [_truncate(t) for t in texts]

    def _summarise_openai(self, texts: list[str]) -> list[str]:
        def _call(text: str) -> str:
            try:
                resp = self._openai.chat.completions.create(
                    model="gpt-4o-mini",
                    messages=[{
                        "role": "user",
                        "content": f"Summarise the following news article in one sentence:\n\n{text[:1000]}",
                    }],
                    max_tokens=80,
                    temperature=0.3,
                )
                return resp.choices[0].message.content.strip()
            except Exception as exc:  # noqa: BLE001
                print(f"[nlp] OpenAI error: {exc}")
                return _truncate(text)

        with ThreadPoolExecutor(max_workers=10) as pool:
            return list(pool.map(_call, texts))


def _truncate(text: str, length: int = 200) -> str:
    text = text.strip()
    if len(text) <= length:
        return text
    cut = text[:length]
    last_space = cut.rfind(" ")
    return (cut[:last_space] if last_space > 0 else cut) + "…"
