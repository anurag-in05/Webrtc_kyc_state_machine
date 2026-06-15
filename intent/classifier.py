"""Ensemble intent classifier.

Encodes the transcript with a multilingual sentence-transformer, hard-votes
three classical classifiers (logistic, linear SVM, MLP); a 1-1-1 split is
broken by the SVM. The four trained classes collapse to three intents:
ambiguous and repeat both fold to 'please_repeat' so the agent re-prompts.

classify_async runs the CPU-bound ensemble off the loop; warmup() loads the
models (+ one throwaway inference) at startup so turn 1 doesn't pay the load.
"""

from __future__ import annotations

import asyncio
from collections import Counter
from pathlib import Path
from typing import Literal

import joblib
from loguru import logger
from sentence_transformers import SentenceTransformer

Intent = Literal["yes", "no", "please_repeat"]

ENCODER_NAME = "paraphrase-multilingual-MiniLM-L12-v2"
MODELS_DIR = Path(__file__).resolve().parent / "models"
ENCODER_DIR = MODELS_DIR / "sentence_encoder"

# Trained label ids → intent. 0 (ambiguous) and 3 (repeat) fold to
# please_repeat: the safe re-prompt default.
LABEL_TO_INTENT: dict[int, Intent] = {
    0: "please_repeat",
    1: "no",
    2: "yes",
    3: "please_repeat",
}


class EnsembleIntentClassifier:
    def __init__(self) -> None:
        self._encoder: SentenceTransformer | None = None
        self._logistic = None
        self._svm = None
        self._mlp = None

    def _ensure_loaded(self) -> None:
        if self._encoder is not None:
            return
        if ENCODER_DIR.exists():
            logger.info(f"Loading cached sentence-transformer encoder from {ENCODER_DIR}")
            self._encoder = SentenceTransformer(str(ENCODER_DIR))
        else:
            logger.info(
                f"Cached encoder not found; downloading {ENCODER_NAME} and saving to {ENCODER_DIR}"
            )
            self._encoder = SentenceTransformer(ENCODER_NAME)
            ENCODER_DIR.parent.mkdir(parents=True, exist_ok=True)
            self._encoder.save(str(ENCODER_DIR))
        self._logistic = joblib.load(MODELS_DIR / "logistic_model.pkl")
        self._svm = joblib.load(MODELS_DIR / "linear_svm_model.pkl")
        self._mlp = joblib.load(MODELS_DIR / "mlp_model.pkl")
        logger.info("Intent ensemble models loaded")

    def warmup(self) -> None:
        """Load all models AND run one throwaway inference. Blocking — call once
        off the loop at startup. The dummy encode matters: the encoder's first
        forward pass is ~600 ms of lazy init, far costlier than loading weights."""
        self._ensure_loaded()
        self._classify_ensemble("warmup")

    def _classify_ensemble(self, text: str) -> Intent:
        """Encode and hard-vote the three models. CPU-bound (~10 ms); run via
        to_thread from async callers."""
        try:
            self._ensure_loaded()
            assert self._encoder is not None
            embedding = self._encoder.encode([text])

            svm_label = int(self._svm.predict(embedding)[0])
            votes = [
                int(self._logistic.predict(embedding)[0]),
                svm_label,
                int(self._mlp.predict(embedding)[0]),
            ]

            counts = Counter(votes).most_common()
            # Tiebreak 1-1-1 with the linear SVM's vote.
            winner = counts[0][0] if counts[0][1] >= 2 else svm_label
            return LABEL_TO_INTENT.get(winner, "please_repeat")
        except Exception as exc:
            logger.exception(f"Intent ensemble failed: {exc}")
            return "please_repeat"

    def classify(self, text: str, language: str = "english") -> Intent:
        text = (text or "").strip()
        if not text:
            return "please_repeat"
        return self._classify_ensemble(text)

    async def classify_async(self, text: str, language: str = "english") -> Intent:
        """Like classify, but runs the ensemble off the loop so media never stalls."""
        text = (text or "").strip()
        if not text:
            return "please_repeat"
        return await asyncio.to_thread(self._classify_ensemble, text)


intent_classifier = EnsembleIntentClassifier()
