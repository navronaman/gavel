CREATE TABLE raw_articles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url  TEXT NOT NULL,
    title       TEXT,
    body        TEXT,
    fetched_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE policy_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    raw_article_id  UUID REFERENCES raw_articles(id),
    entities        JSONB,
    topic           TEXT,
    confidence      FLOAT,
    summary         TEXT,
    analyzed_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_policy_events_topic       ON policy_events(topic);
CREATE INDEX idx_policy_events_analyzed_at ON policy_events(analyzed_at DESC);
