package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/domainengine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestDomainRoutesRejectUnauthorizedSessions(t *testing.T) {
	enforce := true
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}, kbCreator: func(*gin.Context) (string, error) { return "fixture-owner", nil }}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Viewer") == "1" {
			c.Request = c.Request.WithContext(types.WithCaller(c.Request.Context(),
				types.Caller{TenantID: 1, UserID: "fixture-user", Role: types.TenantRoleViewer}))
		}
		c.Next()
	})
	registerDomainRoutes(r.Group("/api/v1"), g, &domainengine.Worker{}, nil)
	for _, tc := range []struct {
		path   string
		viewer bool
	}{
		{"/api/v1/domain-profiles", false},
		{"/api/v1/domain-profiles", true},
		{"/api/v1/domain-profiles/mock/1/package", true},
		{"/api/v1/knowledge-bases/other/domain/jobs", false},
		{"/api/v1/knowledge-bases/other/domain/jobs", true},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.viewer {
			req.Header.Set("X-Test-Viewer", "1")
		}
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d", tc.path, res.Code)
		}
	}
}
