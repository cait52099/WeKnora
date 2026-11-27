package router

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/domainengine"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func registerDomainRoutes(r *gin.RouterGroup, g *rbacGuards, w *domainengine.Worker, knowledge interfaces.KnowledgeService) {
	if w == nil {
		return
	}
	store := w.Store
	session := func(c *gin.Context) {
		if _, key := types.AuditAPIKeyFromContext(c.Request.Context()); key || types.CallerFromContext(c.Request.Context()).UserID == "" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
	tenant := func(c *gin.Context) uint64 { return types.CallerFromContext(c.Request.Context()).TenantID }
	scope := func(c *gin.Context) domainengine.Scope {
		return domainengine.Scope{TenantID: tenant(c), KBID: c.Param("id")}
	}
	reply := func(c *gin.Context, data any, err error) {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			c.JSON(404, gin.H{"message": "Domain resource not found"})
		case errors.Is(err, domainengine.ErrConflict):
			c.JSON(409, gin.H{"message": "Domain resource changed or is not eligible for this action"})
		case err != nil:
			c.JSON(500, gin.H{"message": "Domain operation failed"})
		default:
			c.JSON(200, gin.H{"success": true, "data": data})
		}
	}
	payload := func(c *gin.Context) ([]byte, error) {
		return io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	}
	profiles := r.Group("/domain-profiles", session, g.Admin())
	profiles.GET("", func(c *gin.Context) { p, err := store.ListProfiles(c.Request.Context(), tenant(c)); reply(c, p, err) })
	profiles.GET("/:profile/:revision", func(c *gin.Context) {
		p, err := store.LoadProfile(c.Request.Context(), tenant(c), c.Param("profile"), c.Param("revision"))
		if err != nil {
			reply(c, nil, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(200, "application/json", p.Manifest())
	})
	for _, action := range []string{"validate", "publish"} {
		profiles.POST("/"+action, func(c *gin.Context) {
			raw, err := payload(c)
			if err != nil {
				c.JSON(400, gin.H{"message": "Invalid manifest body"})
				return
			}
			p, err := domainengine.ImportProfile(bytes.NewReader(raw))
			if err != nil {
				c.JSON(400, gin.H{"message": err.Error()})
				return
			}
			if action == "publish" {
				err = store.Publish(c.Request.Context(), tenant(c), p)
			}
			reply(c, gin.H{"sha256": p.Fingerprint()}, err)
		})
	}
	profiles.POST("/preview/:kind", func(c *gin.Context) {
		raw, err := payload(c)
		if err != nil {
			c.JSON(400, gin.H{"message": "Invalid import body"})
			return
		}
		if c.Param("kind") == "example" {
			if err = domainengine.ValidateExample(raw); err != nil {
				c.JSON(400, gin.H{"message": "Invalid JSON example"})
				return
			}
			reply(c, gin.H{"text": string(raw), "warning": "Example only. Required fields, type constraints and scientific meaning need review."}, nil)
			return
		}
		text, err := domainengine.PromptPreview(c.Param("kind"), raw)
		if err != nil {
			c.JSON(400, gin.H{"message": "Invalid or unsupported prompt"})
			return
		}
		reply(c, gin.H{"text": text}, nil)
	})
	group := r.Group("/knowledge-bases/:id/domain", session, g.OwnedKBOrAdmin(), g.KBAccessWrite("id"))
	group.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	group.GET("/binding", func(c *gin.Context) {
		b, err := store.Binding(c.Request.Context(), scope(c))
		if errors.Is(err, sql.ErrNoRows) {
			reply(c, nil, nil)
			return
		}
		reply(c, b, err)
	})
	group.PUT("/binding", func(c *gin.Context) {
		var body struct {
			Profile  string `json:"profile_id"`
			Revision string `json:"revision"`
			Version  int64  `json:"version"`
			Enabled  bool   `json:"enabled"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		if err := c.ShouldBindJSON(&body); err != nil || body.Version < 0 {
			c.Status(400)
			return
		}
		profile, err := store.LoadProfile(c.Request.Context(), tenant(c), body.Profile, body.Revision)
		if err != nil {
			reply(c, nil, err)
			return
		}
		if body.Enabled {
			definition, err := profile.Definition()
			if err != nil {
				reply(c, nil, err)
				return
			}
			for _, stage := range definition.Stages {
				if stage.Optional && !stage.Enabled {
					continue
				}
				if _, err = w.Models.Pin(c.Request.Context(), tenant(c), stage.ModelRef); err != nil {
					c.JSON(400, gin.H{"message": "Each enabled stage requires an available chat model", "stage_id": stage.ID})
					return
				}
			}
		}
		version, err := store.Bind(c.Request.Context(), domainengine.Binding{Scope: scope(c), ProfileID: body.Profile, Revision: body.Revision, Enabled: body.Enabled}, body.Version)
		reply(c, gin.H{"version": version}, err)
	})
	group.GET("/jobs", func(c *gin.Context) { jobs, err := store.ListJobs(c.Request.Context(), scope(c)); reply(c, jobs, err) })
	jobRoutes := group.Group("/jobs/:job", func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("job")); err != nil {
			c.AbortWithStatus(404)
			return
		}
		c.Next()
	})
	jobRoutes.GET("/runs", func(c *gin.Context) {
		runs, err := store.Runs(c.Request.Context(), scope(c), c.Param("job"))
		reply(c, runs, err)
	})
	jobRoutes.GET("/parse", func(c *gin.Context) {
		data, err := store.ParsePreview(c.Request.Context(), scope(c), c.Param("job"))
		reply(c, data, err)
	})
	jobRoutes.GET("/runs/:attempt/:kind", func(c *gin.Context) {
		attempt, err := strconv.ParseInt(c.Param("attempt"), 10, 64)
		kind := c.Param("kind")
		if err != nil || attempt < 1 || (kind != "valid" && kind != "raw") {
			c.Status(404)
			return
		}
		raw, err := store.RunArtifact(c.Request.Context(), scope(c), c.Param("job"), attempt, kind == "valid")
		if err != nil {
			reply(c, nil, err)
			return
		}
		if len(raw) == 0 {
			c.Status(404)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		if c.Query("download") == "1" {
			name := "raw-response.txt"
			if kind == "valid" {
				name = "stage.json"
			}
			c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
		}
		c.Data(200, "text/plain; charset=utf-8", raw)
	})
	jobRoutes.POST("/retry", func(c *gin.Context) {
		ctx := c.Request.Context()
		job, err := store.GetJob(ctx, scope(c), c.Param("job"))
		if err != nil {
			reply(c, nil, err)
			return
		}
		if job.Status == "capture_failed" {
			_, err = knowledge.ReparseKnowledge(ctx, job.DocumentID, nil)
		} else {
			err = store.ChangeJob(ctx, scope(c), job.ID, false)
		}
		reply(c, nil, err)
	})
	jobRoutes.POST("/reprocess", func(c *gin.Context) {
		reply(c, nil, store.Reprocess(c.Request.Context(), scope(c), c.Param("job"), w.Models))
	})

	jobRoutes.POST("/cancel", func(c *gin.Context) {
		reply(c, nil, store.ChangeJob(c.Request.Context(), scope(c), c.Param("job"), true))
	})
}
