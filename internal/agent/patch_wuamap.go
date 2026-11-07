package agent

import (
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Windows Update classification IDs.
var wuaClassifications = map[string]string{
	"0fa1201d-4330-4fa8-8ae9-b877473b6441": agentproto.CategorySecurity,   // Security Updates
	"e6cf1350-c01b-414d-a61f-263d14d133b4": agentproto.CategoryCritical,   // Critical Updates
	"e0789628-ce08-4437-be74-2495b842f43b": agentproto.CategoryDefinition, // Definition Updates
	"28bc880e-0592-4cbf-8f95-c79b17911d5f": agentproto.CategoryCumulative, // Update Rollups
	"ebfc1fc5-71a4-4f7b-9aca-3b9a503104a0": agentproto.CategoryDriver,     // Drivers
	"3689bdc8-b205-4af4-8d4a-a63924c5e9d5": agentproto.CategoryFeature,    // Upgrades
	"b54e7d24-7add-428f-8b75-90a396fa584f": agentproto.CategoryOther,      // Feature Packs
	"68c5b0a3-d1a6-4553-ae49-01d3a7827828": agentproto.CategoryOther,      // Service Packs
	"cd5ffd1e-e932-4e3a-bf74-18bf0b1bbd83": agentproto.CategoryOther,      // Updates
	"b4832bd8-e735-4761-8daf-37f882276dab": agentproto.CategoryOther,      // Tools
}

// wuaCategory maps an update's classifications onto a FleetWarden category.
// Monthly cumulative updates are classified as security updates by
// Microsoft; they get their own category so a policy can wait for them.
func wuaCategory(classIDs []string, title string, driver bool) string {
	if driver {
		return agentproto.CategoryDriver
	}
	found := map[string]bool{}
	for _, id := range classIDs {
		if c, ok := wuaClassifications[strings.ToLower(strings.Trim(id, "{}"))]; ok {
			found[c] = true
		}
	}
	lt := strings.ToLower(title)
	switch {
	case found[agentproto.CategoryFeature]:
		return agentproto.CategoryFeature
	case found[agentproto.CategoryDriver]:
		return agentproto.CategoryDriver
	case found[agentproto.CategoryDefinition]:
		return agentproto.CategoryDefinition
	case strings.Contains(lt, "cumulative update") || found[agentproto.CategoryCumulative]:
		return agentproto.CategoryCumulative
	case found[agentproto.CategorySecurity]:
		return agentproto.CategorySecurity
	case found[agentproto.CategoryCritical]:
		return agentproto.CategoryCritical
	}
	return agentproto.CategoryOther
}

// wuaResultOK reports whether an OperationResultCode means success
// (2 = succeeded, 3 = succeeded with errors).
func wuaResultOK(code int) bool { return code == 2 || code == 3 }

func wuaResultText(code int, hresult int32) string {
	names := map[int]string{0: "not started", 1: "in progress", 2: "succeeded", 3: "succeeded with errors", 4: "failed", 5: "aborted"}
	s := names[code]
	if s == "" {
		s = "unknown result"
	}
	if hresult != 0 {
		s += " (0x" + strings.ToUpper(hex32(uint32(hresult))) + ")"
	}
	return s
}

func hex32(v uint32) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b)
}
