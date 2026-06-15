CREATE TABLE IF NOT EXISTS tasks (
    id text PRIMARY KEY,
    title text NOT NULL CHECK (length(trim(title)) > 0),
    completed boolean NOT NULL DEFAULT false
);
