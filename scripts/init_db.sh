#!/usr/bin/env bash
set -euo pipefail

# VatBrain — Database Initialization Script
# Creates constraints, indexes, and tables for Neo4j + pgvector.

NEO4J_URI="${NEO4J_URI:-bolt://localhost:7687}"
NEO4J_USER="${NEO4J_USER:-neo4j}"
NEO4J_PASS="${NEO4J_PASS:-vatbrain}"

PG_HOST="${PG_HOST:-localhost}"
PG_PORT="${PG_PORT:-5432}"
PG_USER="${PG_USER:-vatbrain}"
PG_PASS="${PG_PASS:-vatbrain}"
PG_DB="${PG_DB:-vatbrain}"

REDIS_HOST="${REDIS_HOST:-localhost}"
REDIS_PORT="${REDIS_PORT:-6379}"

echo "=== VatBrain DB Init ==="

# ── Neo4j ──────────────────────────────────────────────
echo ""
echo "--- Neo4j: constraints ---"

cypher-shell -a "$NEO4J_URI" -u "$NEO4J_USER" -p "$NEO4J_PASS" <<'CYPHER'
CREATE CONSTRAINT episodic_id_unique IF NOT EXISTS
FOR (e:EpisodicMemory) REQUIRE e.id IS UNIQUE;

CREATE CONSTRAINT semantic_id_unique IF NOT EXISTS
FOR (s:SemanticMemory) REQUIRE s.id IS UNIQUE;

CREATE INDEX episodic_project_idx IF NOT EXISTS
FOR (e:EpisodicMemory) ON (e.project_id, e.language);

CREATE INDEX episodic_weight_idx IF NOT EXISTS
FOR (e:EpisodicMemory) ON (e.weight);

CREATE INDEX semantic_type_idx IF NOT EXISTS
FOR (s:SemanticMemory) ON (s.type);
CYPHER

echo "Neo4j constraints created."

# ── PostgreSQL / pgvector ──────────────────────────────
echo ""
echo "--- pgvector: table + index ---"

PGPASSWORD="$PG_PASS" psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" <<'SQL'
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS episodic_embeddings (
    id UUID PRIMARY KEY,
    memory_id UUID NOT NULL,
    embedding vector(1536),
    summary_text TEXT,
    project_id VARCHAR(255),
    language VARCHAR(64),
    task_type VARCHAR(64),
    created_at TIMESTAMPTZ DEFAULT now(),
    metadata JSONB
);

CREATE INDEX IF NOT EXISTS episodic_embeddings_project_idx
    ON episodic_embeddings (project_id, language);

CREATE INDEX IF NOT EXISTS episodic_embeddings_memory_idx
    ON episodic_embeddings (memory_id);
SQL

# IVFFlat index — created separately since it needs data to build centroids.
# Run after seeding data:
#   CREATE INDEX ON episodic_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

echo "pgvector table created (IVFFlat index deferred)."

# ── Redis ──────────────────────────────────────────────
echo ""
echo "--- Redis: ping ---"
redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" PING

echo ""
echo "=== Init complete ==="
