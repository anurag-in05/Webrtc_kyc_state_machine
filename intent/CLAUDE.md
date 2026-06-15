# Intent (Python) — UNCHANGED

Service dir: `intent/`, FastAPI on `:8001`.

**Copy `videokyc-wsfu-dev/services/intent/` verbatim. Do not modify anything.**

```
intent/
├── service.py            # POST /classify, GET /health, warmup on startup
├── classifier.py         # ensemble: MiniLM encoder + LR/SVM/MLP hard-vote
├── __init__.py
├── requirements.txt
├── Dockerfile
└── models/
    ├── logistic_model.pkl
    ├── linear_svm_model.pkl
    └── mlp_model.pkl
    # sentence_encoder/ is downloaded/baked at build (~100 MB), gitignored
```

## What it is (for context only — change nothing)

- `POST /classify {"text":"...","language":"english"}` → `{"intent":"yes|no|please_repeat"}`.
- Encodes the transcript with `paraphrase-multilingual-MiniLM-L12-v2`, hard-votes
  three classifiers (logistic, linear SVM, MLP); 1-1-1 tie broken by the SVM.
- Trained labels fold to three intents: 0 (ambiguous) and 3 (repeat) →
  `please_repeat`, 1 → `no`, 2 → `yes`. Any failure/empty → `please_repeat`.

The brain points `INTENT_SERVICE_URL` at this service. That's the only wiring.

## Why it stays Python and separate

It owns a ~1.5 GB model loaded once and shared. Go never touches ML. This is the
"copy a component as-is" the whole rewrite was designed to allow — leave it be.
