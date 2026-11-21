"""Photostead's AI worker. It pulls tasks from a Photostead server, fetches only the renditions it
needs, runs models with ONNX Runtime (CPU, or a GPU under a Mission Control lease), and posts the
results back. See docs/architecture.md, "Jobs, workers and AI calls"."""

__version__ = "0.1.0"
