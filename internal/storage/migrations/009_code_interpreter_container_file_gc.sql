ALTER TABLE code_interpreter_container_files
    ADD COLUMN delete_backing_file INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_code_interpreter_container_files_backing_file
    ON code_interpreter_container_files(backing_file_id);
