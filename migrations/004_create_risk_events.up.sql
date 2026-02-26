CREATE TABLE IF NOT EXISTS risk_events (
    id          UUID PRIMARY KEY,
    event_type  VARCHAR(32) NOT NULL,
    severity    VARCHAR(4) NOT NULL,
    details     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_risk_events_type ON risk_events(event_type);
CREATE INDEX idx_risk_events_severity ON risk_events(severity);
CREATE INDEX idx_risk_events_created_at ON risk_events(created_at);
