CREATE VIRTUAL TABLE IF NOT EXISTS vector_store_chunks_fts USING fts5(
  vector_store_id UNINDEXED,
  file_id UNINDEXED,
  content,
  tokenize = 'unicode61'
);
