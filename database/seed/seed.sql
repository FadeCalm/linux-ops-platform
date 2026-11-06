-- Minimal seed data for a first run (optional).
-- The backend also seeds equivalent rows when mysql.seed=true and `servers` is empty.
USE linux_ops;

INSERT INTO server_groups (name, description, created_at, updated_at)
VALUES ('default', 'default group', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE name = name;

INSERT INTO server_tags (name, created_at, updated_at)
VALUES ('local', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE name = name;

INSERT INTO servers (name, ip, agent_port, group_id, status, created_at, updated_at)
SELECT 'local-agent', '127.0.0.1', 19090, g.id, 'unknown', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM server_groups g
WHERE g.name = 'default'
  AND NOT EXISTS (SELECT 1 FROM servers WHERE name = 'local-agent');

INSERT IGNORE INTO servers_tags (server_id, server_tag_id)
SELECT s.id, t.id FROM servers s JOIN server_tags t ON t.name = 'local'
WHERE s.name = 'local-agent';
