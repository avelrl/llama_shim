CREATE TABLE IF NOT EXISTS chat_completion_messages (
    completion_id TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    message_id TEXT NOT NULL,
    message_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (completion_id, sequence_number),
    FOREIGN KEY (completion_id) REFERENCES chat_completions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chat_completion_messages_completion_message_id
    ON chat_completion_messages(completion_id, message_id, sequence_number);
