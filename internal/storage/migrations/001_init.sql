CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS responses (
    id TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    request_json TEXT NOT NULL,
    normalized_input_items_json TEXT NOT NULL,
    output_json TEXT NOT NULL,
    output_text TEXT NOT NULL,
    previous_response_id TEXT NULL,
    conversation_id TEXT NULL,
    store INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    completed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_responses_previous_response_id ON responses(previous_response_id);
CREATE INDEX IF NOT EXISTS idx_responses_conversation_id ON responses(conversation_id);
CREATE INDEX IF NOT EXISTS idx_responses_created_at ON responses(created_at);

CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS conversation_items (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    source TEXT NOT NULL,
    role TEXT NULL,
    item_type TEXT NOT NULL,
    item_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    CONSTRAINT uq_conversation_items_seq UNIQUE (conversation_id, seq),
    CONSTRAINT fk_conversation_items_conversation FOREIGN KEY (conversation_id) REFERENCES conversations(id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_items_conversation_seq ON conversation_items(conversation_id, seq);
CREATE INDEX IF NOT EXISTS idx_conversation_items_created_at ON conversation_items(created_at);
