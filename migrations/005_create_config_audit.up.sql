CREATE TABLE IF NOT EXISTS config_audit (
    id          UUID PRIMARY KEY,
    key         VARCHAR(128) NOT NULL,
    old_value   TEXT,
    new_value   TEXT NOT NULL,
    changed_by  VARCHAR(64) NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_config_audit_key ON config_audit(key);
CREATE INDEX idx_config_audit_changed_at ON config_audit(changed_at);
