ALTER TABLE conversations
ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}';
