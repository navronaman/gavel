"""gRPC AnalysisService + FastAPI /health endpoint.

Ports:
  50051 — gRPC (AnalyzeBatch RPC)
  8001  — HTTP (FastAPI /health)

Run with: python main.py
"""

import os
import sys
import signal
import threading
from concurrent import futures

import grpc
import uvicorn
from fastapi import FastAPI

# Add the gen/ directory to sys.path so the protoc-generated imports resolve.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "gen"))
import analysis_pb2          # noqa: E402
import analysis_pb2_grpc     # noqa: E402

from nlp_model import NLPModel  # noqa: E402

# ── FastAPI health endpoint ───────────────────────────────────────────────────

http_app = FastAPI()

@http_app.get("/health")
def health() -> dict:
    return {"status": "ok", "service": "gavel-ai"}


# ── gRPC service implementation ───────────────────────────────────────────────

class AnalysisServicer(analysis_pb2_grpc.AnalysisServiceServicer):
    """Implements AnalyzeBatch using the singleton NLPModel."""

    def __init__(self, model: NLPModel) -> None:
        self._model = model

    def AnalyzeBatch(
        self,
        request: analysis_pb2.BatchRequest,
        context: grpc.ServicerContext,
    ) -> analysis_pb2.BatchResponse:
        texts = [a.text for a in request.articles]

        if not texts:
            return analysis_pb2.BatchResponse()

        results = self._model.analyze_batch(texts)

        proto_results = []
        for r in results:
            entities_proto = {
                group: analysis_pb2.StringList(values=names)
                for group, names in r["entities"].items()
            }
            proto_results.append(analysis_pb2.AnalysisResult(
                entities=entities_proto,
                topic=r["topic"],
                confidence=r["confidence"],
                summary=r["summary"],
            ))

        return analysis_pb2.BatchResponse(results=proto_results)


# ── Server startup ────────────────────────────────────────────────────────────

def serve() -> None:
    # Load models once at startup — both the gRPC handler and the HTTP server
    # reference the same singleton instance.
    model = NLPModel()

    # gRPC server
    grpc_server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=10),
        options=[
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),  # 50 MB
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
        ],
    )
    analysis_pb2_grpc.add_AnalysisServiceServicer_to_server(
        AnalysisServicer(model), grpc_server
    )
    grpc_port = int(os.getenv("GRPC_PORT", "50051"))
    grpc_server.add_insecure_port(f"[::]:{grpc_port}")
    grpc_server.start()
    print(f"[ai-service] gRPC listening on :{grpc_port}")

    # FastAPI / uvicorn in a daemon thread so it doesn't block graceful shutdown
    http_port = int(os.getenv("HTTP_PORT", "8001"))
    http_thread = threading.Thread(
        target=uvicorn.run,
        kwargs={
            "app": http_app,
            "host": "0.0.0.0",
            "port": http_port,
            "log_level": "warning",
        },
        daemon=True,
    )
    http_thread.start()
    print(f"[ai-service] HTTP health endpoint on :{http_port}")

    # Graceful shutdown on SIGTERM / SIGINT
    def _shutdown(signum, frame):  # noqa: ANN001
        print("[ai-service] shutting down…")
        grpc_server.stop(grace=5)
        sys.exit(0)

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)

    grpc_server.wait_for_termination()


if __name__ == "__main__":
    serve()
