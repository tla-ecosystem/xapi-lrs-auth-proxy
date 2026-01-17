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

// ValidateWrite checks if a statement write is allowed
func (v *PermissionValidator) ValidateWrite(claims *models.Claims, stmt *models.Statement) error {
	scope := claims.Permissions.Write

	// No write permission
	if scope == "false" {
		return fmt.Errorf("write permission denied")
	}

	switch scope {
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

	// Activity must match
	if stmt.Object.ID != claims.ActivityID {
		return fmt.Errorf("%s denied: activity mismatch (expected %s, got %s)",
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

	// Activity must match
	if stmt.Object.ID != claims.ActivityID {
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

	// If activity specified, must match
	if activity := query["activity"]; activity != "" {
		if activity != claims.ActivityID {
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

	// Activity must match (if specified)
	if activity := query["activity"]; activity != "" {
		if activity != claims.ActivityID {
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

	// Activity must match (if specified)
	if activity := query["activity"]; activity != "" {
		if activity != claims.ActivityID {
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

	// Actor must match
	if !strings.Contains(agent, claims.Actor.Mbox) &&
		!strings.Contains(agent, claims.Actor.OpenID) {
		return fmt.Errorf("state access denied: agent mismatch")
	}

	// Activity must match (for default scope)
	scope := claims.Permissions.Read
	if scope == "actor-activity-registration-scoped" {
		if activityID != claims.ActivityID {
			return fmt.Errorf("state access denied: activity mismatch")
		}
		if registration != claims.Registration {
			return fmt.Errorf("state access denied: registration mismatch")
		}
	}

	return nil
}
