-- How often agents send their inventory and scan for missing updates.
-- The server-wide defaults live in settings ('scans'); a group may override
-- them. NULL means "use the default".
ALTER TABLE device_groups
    ADD COLUMN inventory_minutes  integer CHECK (inventory_minutes BETWEEN 15 AND 10080),
    ADD COLUMN patch_scan_minutes integer CHECK (patch_scan_minutes BETWEEN 60 AND 10080);

-- The scan interval used to be part of the patch policy. Keep any interval
-- that was changed from the old default as an override on the groups that
-- use the policy, so nothing scans more or less often after the upgrade.
UPDATE device_groups g SET patch_scan_minutes = p.scan_hours * 60
    FROM patch_policies p
    WHERE g.patch_policy_id = p.id AND p.scan_hours <> 6;
