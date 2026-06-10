#!/bin/sh
# init-redis.sh
# Bootstrap script executed ONCE when the redis container is first created.
# Creates the vector index used by the semantic cache.
#
# Idempotent: if the index already exists, FT.CREATE returns an error which we
# ignore. Subsequent container restarts (without deleting the volume) will not
# re-run this script.
#
# Index design:
#   - Name:        cache_idx
#   - Type:        HASH (we store one HASH per cache entry)
#   - Key prefix:  cache:  (filters out non-cache keys)
#   - Vector:      nomic-embed-text embeddings, 768 dims, FLOAT32, COSINE
#   - HNSW M=6:    6 neighbor links per node. Default balance of speed/recall.
#   - Sortable:    created_at (for future TTL/range queries)

set -e

echo "[init-redis] Waiting for Redis to be ready..."
until redis-cli ping > /dev/null 2>&1; do
  sleep 1
done

echo "[init-redis] Creating vector index 'cache_idx'..."

redis-cli FT.CREATE cache_idx \
  ON HASH \
  PREFIX 1 "cache:" \
  SCHEMA \
    query         TEXT \
    embedding     VECTOR HNSW 6 TYPE FLOAT32 DIM 768 DISTANCE_METRIC COSINE \
    response_json TEXT \
    model         TEXT \
    provider      TEXT \
    created_at    NUMERIC SORTABLE \
    hits          NUMERIC \
  > /dev/null

echo "[init-redis] Index 'cache_idx' created successfully."
echo "[init-redis] Verifying..."

redis-cli FT.INFO cache_idx | head -20
echo "[init-redis] Done."
