CREATE TABLE response_replay_artifacts (
    response_id TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (response_id, sequence_number),
    FOREIGN KEY (response_id) REFERENCES responses(id) ON DELETE CASCADE
);

CREATE INDEX idx_response_replay_artifacts_response_id
    ON response_replay_artifacts(response_id, sequence_number);
