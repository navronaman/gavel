FROM python:3.12-slim

WORKDIR /app

# Install dependencies first (layer-cached unless requirements.txt changes).
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Pre-download HuggingFace models into the image so container startup is fast.
RUN python -c "\
from transformers import pipeline; \
pipeline('ner', model='dslim/bert-base-NER', aggregation_strategy='simple'); \
pipeline('zero-shot-classification', model='facebook/bart-large-mnli')"

# gen/ is produced by 'make proto' before 'make up'.
COPY gen/ gen/
COPY *.py .

EXPOSE 50051 8001

CMD ["python", "main.py"]
