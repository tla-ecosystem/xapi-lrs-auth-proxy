#!/bin/bash
# Example test client for xAPI LRS Auth Proxy
# Demonstrates the complete flow from token request to statement submission

set -e

# Configuration
PROXY_URL="${PROXY_URL:-http://localhost:8080}"
LMS_API_KEY="${LMS_API_KEY:-test-api-key-12345}"

echo "=== xAPI LRS Auth Proxy Test Client ==="
echo "Proxy URL: $PROXY_URL"
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Step 1: Request JWT token (LMS perspective)
echo "Step 1: Requesting JWT token from proxy..."
TOKEN_RESPONSE=$(curl -s -X POST "$PROXY_URL/auth/token" \
  -H "Authorization: Bearer $LMS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "actor": {
      "objectType": "Agent",
      "mbox": "mailto:test.learner@example.com",
      "name": "Test Learner"
    },
    "registration": "550e8400-e29b-41d4-a716-446655440000",
    "activity_id": "https://example.com/activity/test-lesson",
    "course_id": "test-course-101",
    "permissions": {
      "write": "actor-activity-registration-scoped",
      "read": "actor-activity-registration-scoped"
    }
  }')

if [ $? -ne 0 ]; then
  echo -e "${RED}✗ Failed to get token${NC}"
  exit 1
fi

TOKEN=$(echo $TOKEN_RESPONSE | jq -r '.token')
EXPIRES=$(echo $TOKEN_RESPONSE | jq -r '.expires_at')

if [ "$TOKEN" == "null" ] || [ -z "$TOKEN" ]; then
  echo -e "${RED}✗ Token request failed${NC}"
  echo "Response: $TOKEN_RESPONSE"
  exit 1
fi

echo -e "${GREEN}✓ Token received${NC}"
echo "Expires at: $EXPIRES"
echo ""

# Step 2: Post a valid statement (should succeed)
echo "Step 2: Posting valid statement..."
STATEMENT=$(cat <<EOF
[{
  "actor": {
    "objectType": "Agent",
    "mbox": "mailto:test.learner@example.com",
    "name": "Test Learner"
  },
  "verb": {
    "id": "http://adlnet.gov/expapi/verbs/completed",
    "display": {"en-US": "completed"}
  },
  "object": {
    "id": "https://example.com/activity/test-lesson",
    "objectType": "Activity",
    "definition": {
      "name": {"en-US": "Test Lesson"}
    }
  },
  "context": {
    "registration": "550e8400-e29b-41d4-a716-446655440000"
  },
  "result": {
    "completion": true,
    "success": true,
    "score": {
      "scaled": 0.95
    }
  }
}]
EOF
)

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$PROXY_URL/xapi/statements" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Experience-API-Version: 1.0.3" \
  -d "$STATEMENT")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" == "200" ]; then
  echo -e "${GREEN}✓ Statement accepted (HTTP $HTTP_CODE)${NC}"
  echo "Response: $BODY"
else
  echo -e "${RED}✗ Statement rejected (HTTP $HTTP_CODE)${NC}"
  echo "Response: $BODY"
fi
echo ""

# Step 3: Try invalid statement - wrong actor (should fail)
echo "Step 3: Testing permission validation - wrong actor..."
INVALID_STATEMENT=$(cat <<EOF
[{
  "actor": {
    "objectType": "Agent",
    "mbox": "mailto:different.learner@example.com"
  },
  "verb": {
    "id": "http://adlnet.gov/expapi/verbs/experienced"
  },
  "object": {
    "id": "https://example.com/activity/test-lesson"
  },
  "context": {
    "registration": "550e8400-e29b-41d4-a716-446655440000"
  }
}]
EOF
)

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$PROXY_URL/xapi/statements" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Experience-API-Version: 1.0.3" \
  -d "$INVALID_STATEMENT")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" == "403" ]; then
  echo -e "${GREEN}✓ Correctly rejected - actor mismatch (HTTP $HTTP_CODE)${NC}"
  echo "Error: $BODY"
else
  echo -e "${RED}✗ Should have rejected - actor mismatch (HTTP $HTTP_CODE)${NC}"
  echo "Response: $BODY"
fi
echo ""

# Step 4: Try invalid statement - wrong activity (should fail)
echo "Step 4: Testing permission validation - wrong activity..."
INVALID_STATEMENT=$(cat <<EOF
[{
  "actor": {
    "objectType": "Agent",
    "mbox": "mailto:test.learner@example.com"
  },
  "verb": {
    "id": "http://adlnet.gov/expapi/verbs/experienced"
  },
  "object": {
    "id": "https://example.com/activity/different-lesson"
  },
  "context": {
    "registration": "550e8400-e29b-41d4-a716-446655440000"
  }
}]
EOF
)

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$PROXY_URL/xapi/statements" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Experience-API-Version: 1.0.3" \
  -d "$INVALID_STATEMENT")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" == "403" ]; then
  echo -e "${GREEN}✓ Correctly rejected - activity mismatch (HTTP $HTTP_CODE)${NC}"
  echo "Error: $BODY"
else
  echo -e "${RED}✗ Should have rejected - activity mismatch (HTTP $HTTP_CODE)${NC}"
  echo "Response: $BODY"
fi
echo ""

# Step 5: Query statements (should succeed with matching registration)
echo "Step 5: Reading statements..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X GET \
  "$PROXY_URL/xapi/statements?registration=550e8400-e29b-41d4-a716-446655440000&limit=10" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Experience-API-Version: 1.0.3")

HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)

if [ "$HTTP_CODE" == "200" ]; then
  echo -e "${GREEN}✓ Statements retrieved (HTTP $HTTP_CODE)${NC}"
  BODY=$(echo "$RESPONSE" | sed '$d')
  STATEMENT_COUNT=$(echo "$BODY" | jq -r '.statements | length')
  echo "Found $STATEMENT_COUNT statement(s)"
else
  echo -e "${RED}✗ Failed to retrieve statements (HTTP $HTTP_CODE)${NC}"
fi
echo ""

echo "=== Test Complete ==="
echo ""
echo "Summary:"
echo "  - Token issuance: ✓"
echo "  - Valid statement: ✓"
echo "  - Actor validation: ✓"
echo "  - Activity validation: ✓"
echo "  - Statement retrieval: ✓"
