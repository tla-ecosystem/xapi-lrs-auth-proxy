package models

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenRequest represents a request to issue a JWT token
type TokenRequest struct {
	Actor        Actor                  `json:"actor"`
	Registration string                 `json:"registration"`
	ActivityID   string                 `json:"activity_id"`
	CourseID     string                 `json:"course_id,omitempty"`
	Permissions  Permissions            `json:"permissions"`
	Group        *Group                 `json:"group,omitempty"` // For group-scoped permissions
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// TokenResponse represents the response containing a JWT token
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Permissions represents xAPI access permissions
type Permissions struct {
	Write string `json:"write"` // e.g., "actor-activity-registration-scoped"
	Read  string `json:"read"`  // e.g., "actor-course-registration-scoped"
}

// Claims represents JWT claims
type Claims struct {
	TenantID     string                 `json:"tenant_id"`
	Actor        Actor                  `json:"actor"`
	Registration string                 `json:"registration"`
	ActivityID   string                 `json:"activity_id"`
	CourseID     string                 `json:"course_id,omitempty"`
	Permissions  Permissions            `json:"permissions"`
	Group        *Group                 `json:"group,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	jwt.RegisteredClaims
}

// Actor represents an xAPI actor
type Actor struct {
	ObjectType string            `json:"objectType,omitempty"`
	Name       string            `json:"name,omitempty"`
	Mbox       string            `json:"mbox,omitempty"`
	MboxSHA1   string            `json:"mbox_sha1sum,omitempty"`
	OpenID     string            `json:"openid,omitempty"`
	Account    *Account          `json:"account,omitempty"`
}

// Account represents an xAPI account
type Account struct {
	HomePage string `json:"homePage"`
	Name     string `json:"name"`
}

// Group represents an xAPI group actor
type Group struct {
	ObjectType string  `json:"objectType"` // Should be "Group"
	Name       string  `json:"name"`
	Member     []Actor `json:"member"`
}

// Statement represents an xAPI statement (simplified)
type Statement struct {
	ID      string                 `json:"id,omitempty"`
	Actor   Actor                  `json:"actor"`
	Verb    Verb                   `json:"verb"`
	Object  Object                 `json:"object"`
	Context *Context               `json:"context,omitempty"`
	Result  map[string]interface{} `json:"result,omitempty"`
}

// Verb represents an xAPI verb
type Verb struct {
	ID      string            `json:"id"`
	Display map[string]string `json:"display,omitempty"`
}

// Object represents an xAPI object
type Object struct {
	ObjectType string                 `json:"objectType,omitempty"`
	ID         string                 `json:"id"`
	Definition map[string]interface{} `json:"definition,omitempty"`
}

// Context represents an xAPI context
type Context struct {
	Registration string                 `json:"registration,omitempty"`
	Instructor   *Actor                 `json:"instructor,omitempty"`
	Team         *Group                 `json:"team,omitempty"`
	Extensions   map[string]interface{} `json:"extensions,omitempty"`
}

// Equals compares two actors for equality
func (a *Actor) Equals(other Actor) bool {
	if a.Mbox != "" && other.Mbox != "" {
		return a.Mbox == other.Mbox
	}
	if a.MboxSHA1 != "" && other.MboxSHA1 != "" {
		return a.MboxSHA1 == other.MboxSHA1
	}
	if a.OpenID != "" && other.OpenID != "" {
		return a.OpenID == other.OpenID
	}
	if a.Account != nil && other.Account != nil {
		return a.Account.HomePage == other.Account.HomePage &&
			a.Account.Name == other.Account.Name
	}
	return false
}

// IsMember checks if an actor is a member of a group
func (g *Group) IsMember(actor Actor) bool {
	for _, member := range g.Member {
		if member.Equals(actor) {
			return true
		}
	}
	return false
}

// ValidatePermission checks if a permission scope is valid
func ValidatePermission(scope string) error {
	validScopes := map[string]bool{
		"actor-activity-registration-scoped":  true,
		"actor-course-registration-scoped":    true,
		"actor-activity-all-registrations":    true,
		"actor-cross-course-certification":    true,
		"group-activity-registration-scoped":  true,
		"course-aggregate-only":               true,
		"course-peer-shared":                  true,
		"false":                               true, // No permission
	}

	if !validScopes[scope] {
		return fmt.Errorf("invalid permission scope: %s", scope)
	}
	return nil
}

// PermissionLevel returns a numeric level for permission comparison
func PermissionLevel(scope string) int {
	levels := map[string]int{
		"false":                               0,
		"actor-activity-registration-scoped":  1,
		"actor-course-registration-scoped":    2,
		"actor-activity-all-registrations":    3,
		"group-activity-registration-scoped":  3,
		"actor-cross-course-certification":    4,
		"course-peer-shared":                  5,
		"course-aggregate-only":               6,
	}
	return levels[scope]
}
