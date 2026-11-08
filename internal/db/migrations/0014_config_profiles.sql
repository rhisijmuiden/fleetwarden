-- Configuration profiles bundle configurations. A configuration can be in
-- several profiles; a profile is assigned to device groups, and every device
-- in those groups gets all configurations of the profile.
--
-- configurations.group_ids stays as the groups a configuration reaches
-- through its enabled profiles. The store keeps it up to date whenever a
-- profile changes, so the queries for targets and counts stay simple.

CREATE TABLE configuration_profiles (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    group_ids   bigint[] NOT NULL DEFAULT '{}',
    created_by  text NOT NULL DEFAULT '',
    updated_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX configuration_profiles_name ON configuration_profiles (lower(name));

CREATE TABLE configuration_profile_items (
    profile_id       bigint NOT NULL REFERENCES configuration_profiles (id) ON DELETE CASCADE,
    configuration_id bigint NOT NULL REFERENCES configurations (id) ON DELETE CASCADE,
    PRIMARY KEY (profile_id, configuration_id)
);
CREATE INDEX configuration_profile_items_config ON configuration_profile_items (configuration_id);

-- Every configuration that was assigned to groups gets a profile of its own
-- with the same name and groups, so nothing changes for the devices.
INSERT INTO configuration_profiles (name, description, group_ids, created_by, updated_by)
SELECT c.name, '', c.group_ids, c.created_by, c.updated_by FROM configurations c WHERE cardinality(c.group_ids) > 0;
INSERT INTO configuration_profile_items (profile_id, configuration_id)
SELECT p.id, c.id FROM configurations c JOIN configuration_profiles p ON lower(p.name) = lower(c.name) WHERE cardinality(c.group_ids) > 0;
