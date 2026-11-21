# Photostead's AI worker for a machine with an NVIDIA GPU: search embeddings, faces, pets, adult
# content and text in pictures on CUDA, and videos' playable copies with NVENC. Needs the NVIDIA
# Container Toolkit:
#   docker run -d --gpus all -e NVIDIA_DRIVER_CAPABILITIES=compute,video,utility \
#     -e PHOTOSTEAD_SERVER=http://<server>:4800 -e PHOTOSTEAD_WORKER_TOKEN=<from `photostead worker add`> \
#     -v photostead-models:/models ghcr.io/<owner>/photostead-worker
# Add -e MISSION_CONTROL_URL=… to take turns with other apps on the GPU through Mission Control.
# Build from the repository root:  docker build -f deploy/docker/worker.Dockerfile -t photostead-worker .
FROM python:3.13-slim-trixie
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
COPY worker/ /src/worker/
RUN pip install --no-cache-dir "/src/worker[gpu]" && rm -rf /src
ENV PHOTOSTEAD_MODELS_DIR=/models PHOTOSTEAD_WORKER_TMP=/tmp NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
VOLUME ["/models"]
ENTRYPOINT ["photostead-worker", "run", "--gpu"]
