package routes

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

// Verify every authentication attachment in the gateway inventory includes the gate.
func TestRequestApprovalEveryAuthChain(t *testing.T) {
	b, err := os.ReadFile("gateway.go")
	require.NoError(t, err)
	count := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "gin.HandlerFunc(apiKeyAuth)") || strings.Contains(line, ".Use(middleware.APIKeyAuthWithSubscriptionGoogle(") {
			count++
			require.Contains(t, line, "approvalGate", line)
		}
	}
	require.Equal(t, 7, count)
}
