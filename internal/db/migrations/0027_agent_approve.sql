-- Agent updates by approval: an approved version goes to every group at once
-- instead of ring by ring.

ALTER TABLE agent_rollouts ADD COLUMN all_groups boolean NOT NULL DEFAULT false;
