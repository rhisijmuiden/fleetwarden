-- Browser updates: a deployment that brings Chrome, Edge and/or Firefox to
-- the newest version from the winget catalog. The agent asks the user,
-- closes the browsers, updates them and opens them again with their tabs.
ALTER TABLE software_deployments DROP CONSTRAINT software_deployments_action_check;
ALTER TABLE software_deployments ADD CONSTRAINT software_deployments_action_check
    CHECK (action IN ('install', 'uninstall', 'browsers'));
ALTER TABLE software_deployments ADD COLUMN browsers text[] NOT NULL DEFAULT '{}';
