package auth

// Role is a FleetWarden role. Roles are fixed; local accounts get one assigned
// directly, Entra accounts get one through role mappings.
type Role string

// Built-in roles, from most to least privileged.
const (
	RoleAdministrator Role = "administrator"
	RoleTechnician    Role = "technician"
	RoleReadOnly      Role = "readonly"
)

// Permission is one capability checked by the API.
type Permission string

// Permissions.
const (
	PermDashboardView  Permission = "dashboard.view"
	PermDevicesView    Permission = "devices.view"
	PermDevicesManage  Permission = "devices.manage"
	PermUsersManage    Permission = "users.manage"
	PermSettingsManage Permission = "settings.manage"
	PermAuditView      Permission = "audit.view"
	PermSystemUpdate   Permission = "system.update"
	PermBackupManage   Permission = "backup.manage"
	PermGroupsManage   Permission = "groups.manage"
	PermAgentsManage   Permission = "agents.manage" // agent releases and update rings
)

// Roles lists the roles in order of privilege.
var Roles = []Role{RoleAdministrator, RoleTechnician, RoleReadOnly}

var rolePermissions = map[Role][]Permission{
	RoleAdministrator: {PermDashboardView, PermDevicesView, PermDevicesManage, PermUsersManage, PermSettingsManage, PermAuditView, PermSystemUpdate, PermBackupManage, PermGroupsManage, PermAgentsManage},
	RoleTechnician:    {PermDashboardView, PermDevicesView, PermDevicesManage},
	RoleReadOnly:      {PermDashboardView, PermDevicesView},
}

// ValidRole reports whether r is a known role.
func ValidRole(r Role) bool {
	_, ok := rolePermissions[r]
	return ok
}

// PermissionsOf returns the permissions of a role.
func PermissionsOf(r Role) []Permission { return rolePermissions[r] }

// Has reports whether role r grants p.
func (r Role) Has(p Permission) bool {
	for _, q := range rolePermissions[r] {
		if q == p {
			return true
		}
	}
	return false
}

// Rank returns the privilege rank: lower is more privileged; unknown roles rank last.
func (r Role) Rank() int {
	for i, q := range Roles {
		if q == r {
			return i
		}
	}
	return len(Roles)
}
