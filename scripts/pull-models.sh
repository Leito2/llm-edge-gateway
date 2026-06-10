#!/bin/sh
# pull-models.sh
# Downloads the Ollama models used by the gateway.
# Run once after installing Ollama.
#
# Models:
#   - nomic-embed-text  -> embedding model, 768 dims, ~274 MB, runs on GPU
#   - gemma3:1b         -> local fallback LLM, ~800 MB on disk, runs on CPU
#                          (gemma4:e4b is too large for 4GB VRAM/8GB RAM;
#                           gemma3:1b provides ~65 tok/s on this hardware)
#
# Hardware note: with 4GB VRAM, embeddings go to GPU and gemma3:1b goes to CPU
# (set OLLAMA_NUM_GPU=0 in the Ollama service to enforce this).

set -e

echo "[pull-models] Pulling nomic-embed-text (embeddings, GPU)..."
ollama pull nomic-embed-text

echo "[pull-models] Pulling gemma3:1b (fallback, CPU)..."
ollama pull gemma3:1b

echo "[pull-models] Verifying installed models..."
ollama list

echo "[pull-models] Done."
