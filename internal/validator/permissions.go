package validator

import (
	"fmt"
	"strings"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/models"
)

// PermissionValidator validates statements against JWT permissions
type PermissionValidator struct {
	policy string // "strict" or "permissive"
}

// NewPermissionValidator creates a new validator
func NewPermissionValidator(policy string) *PermissionValidator {
	return &PermissionValidator{
		policy: policy,
	}
}

// activityMatches reports whether an observed activity IRI is within the
// scope of a credential's authorized activityID. An exact match always
// qualifies; a credential is also authorized for any sub-activity nested
// under it via a '#fragment' or '/path' suffix. AU-authored interaction
// statements (answered/interacted) commonly target the AU's own activity ID
// plus a tool-generated suffix rather than the bare AU activity ID itself -
// e.g. Storyline's cmi5 export reports quiz questions against
// "<activityId>#Scene1_Slide2_TrueFalse_0_0". Requiring a byte-for-byte
// match here denied a registration-scoped AU credential write access to its
// own interaction statements with "activity mismatch" - confirmed live
// against a real Storyline cmi5 export on 2026-08-31 (403 on every
// `answered` statement, queued for retry, later statements never flushing
// either). See the cmi5 implementation plan.
func activityMatches(want, got string) bool {
	if got == want {
		return true
	}
	return strings.HasPrefix(got, want+"#") || strings.HasPrefix(got, want+"/")
}

// ValidateWrite checks if a statement write is allowed
func (v *PermissionValidator) ValidateWrite(claims *models.Claims, stmt *models.Statement) error {
	scope := claims.Permissions.Write

	// No write permission
	if scope == "false" {
		return fmt.Errorf("write permission denied")
	}

	switch scope {
	case "unrestricted-passthrough":
		// Full-access test/admin credential (see PassthroughKeys) — bypasses
		// actor/activity/registration scoping entirely, same privilege as the
		// raw LRS credential.
		return nil

	case "actor-activity-registration-scoped":
		return v.validateActorActivityRegistration(claims, stmt, "write")

	case "group-activity-registration-scoped":
		return v.validateGroupActivityRegistration(claims, stmt)

	default:
		return fmt.Errorf("unsupported write permission scope: %s", scope)
	}
}

// ValidateRead checks if a statement read is allowed (query validation)
func (v *PermissionValidator) ValidateRead(claims *models.Claims, query map[string]string) error {
	scope := claims.Permissions.Read

	// No read permission
	if scope == "false" {
		return fmt.Errorf("read permission denied")
	}

	switch scope {
	case "unrestricted-passthrough":
		// Full-access test/admin credential — see ValidateWrite.
		return nil

	case "actor-activity-registration-scoped":
		return v.validateActorActivityRegistrationRead(claims, query)

	case "actor-course-registration-scoped":
		return v.validateActorCourseRegistrationRead(claims, query)

	case "actor-activity-all-registrations":
		return v.validateActorActivityAllRegistrationsRead(claims, query)

	case "group-activity-registration-scoped":
		return v.validateGroupActivityRegistrationRead(claims, query)

	default:
		if v.policy == "permissive" {
			// In permissive mode, allow unknown scopes but log warning
			return nil
		}
		return fmt.Errorf("unsupported read permission scope: %s", scope)
	}
}

// validateActorActivityRegistration validates default cmi5 isolation
func (v *PermissionValidator) validateActorActivityRegistration(claims *models.Claims, stmt *models.Statement, op string) error {
	// Actor must match
	if !claims.Actor.Equals(stmt.Actor) {
		return fmt.Errorf("%s denied: actor mismatch (expected %v, got %v)",
			op, claims.Actor, stmt.Actor)
	}

	// Activity must match (exact match, or a sub-activity nested under it -
	// see activityMatches)
	if !activityMatches(claims.ActivityID, stmt.Object.ID) {
		return fmt.Errorf("%s denied: activity mismatch (expected %s or a sub-activity of it, got %s)",
			op, claims.ActivityID, stmt.Object.ID)
	}

	// Registration must match
	if stmt.Context == nil || stmt.Context.Registration != claims.Registration {
		return fmt.Errorf("%s denied: registration mismatch (expected %s, got %v)",
			op, claims.Registration, stmt.Context)
	}

	return nil
}

// validateGroupActivityRegistration validates group-scoped permissions
func (v *PermissionValidator) validateGroupActivityRegistration(claims *models.Claims, stmt *models.Statement) error {
	// Statement must use Group actor
	if stmt.Actor.ObjectType != "Group" {
		return fmt.Errorf("write denied: group actor required")
	}

	// Group must match authorized group
	if claims.Group == nil || stmt.Actor.Name != claims.Group.Name {
		return fmt.Errorf("write denied: group mismatch")
	}

	// Requesting actor must be a group member
	if !claims.Group.IsMember(claims.Actor) {
		return fmt.Errorf("write denied: actor not a member of group")
	}

	// Activity must match (exact match, or a sub-activity nested under it -
	// see activityMatches)
	if !activityMatches(claims.ActivityID, stmt.Object.ID) {
		return fmt.Errorf("write denied: activity mismatch")
	}

	// Registration must match
	if stmt.Context == nil || stmt.Context.Registration != claims.Registration {
		return fmt.Errorf("write denied: registration mismatch")
	}

	return nil
}

// validateActorActivityRegistrationRead validates read with default isolation
func (v *PermissionValidator) validateActorActivityRegistrationRead(claims *models.Claims, query map[string]string) error {
	// If agent specified in query, must match
	if agent := query["agent"]; agent != "" {
		// Simplified check - in production, parse full agent JSON
		if !strings.Contains(agent, claims.Actor.Mbox) &&
			!strings.Contains(agent, claims.Actor.OpenID) {
			return fmt.Errorf("read denied: agent mismatch")
		}
	}

	// If activity specified, must match (exact match, or a sub-activity
	// nested under it - see activityMatches)
	if activity := query["activity"]; activity != "" {
		if !activityMatches(claims.ActivityID, activity) {
			return fmt.Errorf("read denied: activity mismatch")
		}
	}

	// If registration specified, must match
	if reg := query["registration"]; reg != "" {
		if reg != claims.Registration {
			return fmt.Errorf("read denied: registration mismatch")
		}
	}

	return nil
}

// validateActorCourseRegistrationRead validates read across course
func (v *PermissionValidator) validateActorCourseRegistrationRead(claims *models.Claims, query map[string]string) error {
	// Actor must match (if specified)
	if agent := query["agent"]; agent != "" {
		if !strings.Contains(agent, claims.Actor.Mbox) &&
			!strings.Contains(agent, claims.Actor.OpenID) {
			return fmt.Errorf("read denied: agent mismatch")
		}
	}

	// Registration must match (if specified)
	if reg := query["registration"]; reg != "" {
		if reg != claims.Registration {
			return fmt.Errorf("read denied: registration mismatch")
		}
	}

	// Activity can be any in course (not validated here - requires course manifest)
	// In production, you'd check if activity belongs to course

	return nil
}

// validateActorActivityAllRegistrationsRead validates read across registrations
func (v *PermissionValidator) validateActorActivityAllRegistrationsRead(claims *models.Claims, query map[string]string) error {
	// Actor must match
	if agent := query["agent"]; agent != "" {
		if !strings.Contains(agent, claims.Actor.Mbox) &&
			!strings.Contains(agent, claims.Actor.OpenID) {
			return fmt.Errorf("read denied: agent mismatch")
		}
	}

	// Activity must match (if specified; exact match, or a sub-activity
	// nested under it - see activityMatches)
	if activity := query["activity"]; activity != "" {
		if !activityMatches(claims.ActivityID, activity) {
			return fmt.Errorf("read denied: activity mismatch")
		}
	}

	// Registration can be any (not validated)

	return nil
}

// validateGroupActivityRegistrationRead validates group read
func (v *PermissionValidator) validateGroupActivityRegistrationRead(claims *models.Claims, query map[string]string) error {
	// Similar to group write validation
	// Group member can read group activity data

	// Activity must match (if specified; exact match, or a sub-activity
	// nested under it - see activityMatches)
	if activity := query["activity"]; activity != "" {
		if !activityMatches(claims.ActivityID, activity) {
			return fmt.Errorf("read denied: activity mismatch")
		}
	}

	// Registration must match (if specified)
	if reg := query["registration"]; reg != "" {
		if reg != claims.Registration {
			return fmt.Errorf("read denied: registration mismatch")
		}
	}

	return nil
}

// ValidateStateAccess validates access to state API
func (v *PermissionValidator) ValidateStateAccess(claims *models.Claims, activityID, agent, registration string) error {
	// State API uses same scoping as statements
	// Simplified validation - in production, parse full agent JSON

	// Full-access test/admin credential — see ValidateWrite.
	if claims.Permissions.Read == "unrestricted-passthrough" || claims.Permissions.Write == "unrestricted-passthrough" {
		return nil
	}

	// Actor must match
	if !strings.Contains(agent, claims.Actor.Mbox) &&
		!strings.Contains(agent, claims.Actor.OpenID) {
		return fmt.Errorf("state access denied: agent mismatch")
	}

	// Activity must match (for default scope)
	scope := claims.Permissions.Read
	if scope == "actor-activity-registration-scoped" {
		if !activityMatches(claims.ActivityID, activityID) {
			return fmt.Errorf("state access denied: activity mismatch")
		}
		// registration is only checked when the caller actually sent one.
		// Unlike the cmi5-mandated LMS.LaunchData read (which cmi5.js always
		// sends with a registration param), generic State/Agent-Profile
		// calls a driver makes for its own bookkeeping (language
		// preference, bookmarking/"status", etc.) commonly omit it - that's
		// valid per the xAPI State Resource spec, registration there is
		// optional context, not a required match key. Treating an absent
		// registration as claims.Registration ("" != a real GUID) rejected
		// every one of those calls with a false "registration mismatch"
		// 403, even though the JWT itself is already scoped to this exact
		// actor/activity/registration - matches the "only check if present"
		// pattern already used by the sibling *Read validators above.
		if registration != "" && registration != claims.Registration {
			return fmt.Errorf("state access denied: registration mismatch")
		}
	}

	return nil
}
