-- Linux 集中运维平台 — M1-M3 schema (MySQL 8.0, utf8mb4, UTC times)
--
-- Two supported paths:
--   1) Default: the backend runs GORM AutoMigrate on startup (mysql.auto_migrate=true).
--   2) Manual/DBA: run this file once against an empty database and set
--      mysql.auto_migrate=false. Do not combine both for the same database
--      unless you accept GORM's additional indexes/constraints.
--
-- Phase 1 deliberately creates NO authentication tables (sys_users, sys_roles,
-- sys_permissions, tokens, certificates). See docs/architecture.md.

CREATE DATABASE IF NOT EXISTS linux_ops
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE linux_ops;

-- ---------------------------------------------------------------------------
-- groups & tags
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS server_groups (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  created_at  DATETIME(3) NOT NULL,
  updated_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_server_groups_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS server_tags (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name       VARCHAR(64) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_server_tags_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- servers & agents
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS servers (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name              VARCHAR(128) NOT NULL,
  ip                VARCHAR(128) NOT NULL,
  agent_port        INT NOT NULL DEFAULT 19090,
  hostname          VARCHAR(255) NOT NULL DEFAULT '',
  os                VARCHAR(64) NOT NULL DEFAULT '',
  arch              VARCHAR(64) NOT NULL DEFAULT '',
  group_id          BIGINT UNSIGNED NULL,
  status            VARCHAR(32) NOT NULL DEFAULT 'unknown',
  agent_version     VARCHAR(64) NOT NULL DEFAULT '',
  last_heartbeat_at DATETIME(3) NULL,
  created_at        DATETIME(3) NOT NULL,
  updated_at        DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_servers_group (group_id),
  KEY idx_servers_status (status),
  CONSTRAINT fk_servers_group FOREIGN KEY (group_id) REFERENCES server_groups (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS agents (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id    BIGINT UNSIGNED NOT NULL,
  version      VARCHAR(64) NOT NULL DEFAULT '',
  os           VARCHAR(64) NOT NULL DEFAULT '',
  arch         VARCHAR(64) NOT NULL DEFAULT '',
  hostname     VARCHAR(255) NOT NULL DEFAULT '',
  extra_json   TEXT NULL,
  last_seen_at DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_agents_server (server_id),
  CONSTRAINT fk_agents_server FOREIGN KEY (server_id) REFERENCES servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS servers_tags (
  server_id     BIGINT UNSIGNED NOT NULL,
  server_tag_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (server_id, server_tag_id),
  KEY idx_servers_tags_tag (server_tag_id),
  CONSTRAINT fk_servers_tags_server FOREIGN KEY (server_id) REFERENCES servers (id) ON DELETE CASCADE,
  CONSTRAINT fk_servers_tags_tag FOREIGN KEY (server_tag_id) REFERENCES server_tags (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- execution tasks (parent = batch, child = per-server target)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS execution_tasks (
  id              CHAR(36) NOT NULL,
  command         MEDIUMTEXT NOT NULL,
  work_dir        VARCHAR(512) NOT NULL DEFAULT '',
  timeout_ms      BIGINT NOT NULL,
  status          VARCHAR(32) NOT NULL,
  total           INT NOT NULL DEFAULT 0,
  succeeded       INT NOT NULL DEFAULT 0,
  failed          INT NOT NULL DEFAULT 0,
  cancelled       INT NOT NULL DEFAULT 0,
  idempotency_key VARCHAR(128) NULL,
  created_by      VARCHAR(64) NOT NULL DEFAULT 'anonymous',
  created_at      DATETIME(3) NOT NULL,
  finished_at     DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_exec_tasks_idem (idempotency_key),
  KEY idx_exec_tasks_status (status),
  KEY idx_exec_tasks_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS execution_task_targets (
  id            CHAR(36) NOT NULL,
  parent_task_id CHAR(36) NOT NULL,
  server_id     BIGINT UNSIGNED NOT NULL,
  server_name   VARCHAR(128) NOT NULL DEFAULT '',
  agent_addr    VARCHAR(255) NOT NULL DEFAULT '',
  status        VARCHAR(32) NOT NULL,
  exit_code     INT NULL,
  duration_ms   BIGINT NOT NULL DEFAULT 0,
  error         TEXT NULL,
  log_dir       VARCHAR(512) NOT NULL DEFAULT '',
  stdout_offset BIGINT NOT NULL DEFAULT 0,
  stderr_offset BIGINT NOT NULL DEFAULT 0,
  seq           BIGINT NOT NULL DEFAULT 0,
  started_at    DATETIME(3) NULL,
  finished_at   DATETIME(3) NULL,
  created_at    DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_targets_parent_server (parent_task_id, server_id),
  KEY idx_targets_status (status),
  KEY idx_targets_server (server_id),
  CONSTRAINT fk_targets_parent FOREIGN KEY (parent_task_id) REFERENCES execution_tasks (id) ON DELETE CASCADE
  -- NOTE: no FK to servers on purpose: server_name/agent_addr are snapshotted so
  -- history survives server deletion; the API blocks deleting hosts with active tasks.
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS execution_log_indexes (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_id   CHAR(36) NOT NULL,
  stream      VARCHAR(16) NOT NULL,
  file_path   VARCHAR(512) NOT NULL,
  byte_offset BIGINT NOT NULL DEFAULT 0,
  length      BIGINT NOT NULL DEFAULT 0,
  seq         BIGINT NOT NULL DEFAULT 0,
  created_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_log_index_target (target_id, stream, byte_offset),
  CONSTRAINT fk_log_index_target FOREIGN KEY (target_id) REFERENCES execution_task_targets (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- audit (operator is always 'anonymous' in phase 1; never forged)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_events (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  action      VARCHAR(64) NOT NULL,
  resource    VARCHAR(64) NOT NULL DEFAULT '',
  resource_id VARCHAR(128) NOT NULL DEFAULT '',
  operator    VARCHAR(64) NOT NULL DEFAULT 'anonymous',
  detail_json TEXT NULL,
  created_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_audit_action (action),
  KEY idx_audit_created (created_at),
  KEY idx_audit_resource (resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
