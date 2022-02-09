DROP TABLE IF EXISTS event_types;
CREATE TABLE event_types
(
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    name       VARCHAR(255),
    account_id INTEGER
);

DROP TABLE IF EXISTS languages;
CREATE TABLE languages
(
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    code       VARCHAR(255)
);