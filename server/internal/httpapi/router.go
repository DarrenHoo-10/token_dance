package httpapi

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/device"
	"tokendance/internal/export"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/search"
)

func NewRouter(
	authService *auth.Service,
	profileService *profile.Service,
	privacyService *privacy.Service,
	analyticsService *analytics.Service,
	deviceService *device.Service,
	exportService *export.Service,
	mediaService *media.Service,
	searchService *search.Service,
	leaderboardService *leaderboard.Service,
) *chi.Mux {
	return NewRouterWithReadiness(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
		nil,
	)
}

func NewRouterWithReadiness(
	authService *auth.Service,
	profileService *profile.Service,
	privacyService *privacy.Service,
	analyticsService *analytics.Service,
	deviceService *device.Service,
	exportService *export.Service,
	mediaService *media.Service,
	searchService *search.Service,
	leaderboardService *leaderboard.Service,
	readinessChecker func(ctx context.Context) error,
) *chi.Mux {
	r := chi.NewRouter()

	mw := NewMiddleware(authService)
	handlers := NewHandlersWithReadiness(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
		readinessChecker,
	)

	// Global Middlewares
	r.Use(mw.RequestIDMiddleware)
	r.Use(chimw.RealIP)
	r.Use(mw.LoggerMiddleware)
	r.Use(mw.Recoverer)
	r.Use(mw.SessionResolver)

	// Health routes (unversioned)
	r.Get("/healthz", handlers.Healthz)
	r.Get("/readyz", handlers.Readyz)

	// Collector /v1 endpoints
	r.Route("/v1", func(cr chi.Router) {
		cr.Use(mw.RateLimit(120, time.Minute))
		cr.Post("/installations/claim", handlers.ClaimInstallation)
		cr.Post("/installations/register", handlers.RegisterInstallation)
		cr.Post("/telemetry/batches", handlers.IngestTelemetry)
		cr.Post("/telemetry/ingest", handlers.IngestTelemetry)
	})

	// User Web API /api/v1
	r.Route("/api/v1", func(api chi.Router) {
		// Public Auth routes
		api.Route("/auth", func(ar chi.Router) {
			ar.Use(mw.RateLimit(60, time.Minute))
			ar.Post("/register/code", handlers.RequestRegisterCode)
			ar.Post("/register", handlers.Register)
			ar.Post("/login", handlers.Login)
			ar.Get("/session", handlers.GetSession)
			ar.Post("/password/code", handlers.RequestPasswordReset)
			ar.Post("/password/reset", handlers.ResetPassword)

			// Protected Auth routes
			ar.Group(func(pr chi.Router) {
				pr.Use(mw.RequireAuth)
				pr.Use(mw.RequireCSRF)
				pr.Post("/logout", handlers.Logout)
				pr.Get("/sessions", handlers.ListSessions)
				pr.Delete("/sessions/{id}", handlers.RevokeSessionByID)
				pr.Post("/sessions/revoke-others", handlers.RevokeOtherSessions)
			})
		})

		// Public Routes (no auth required)
		api.Route("/public", func(pr chi.Router) {
			pr.Get("/users/{handle}", handlers.GetPublicProfile)
			pr.Get("/users/{handle}/trends", handlers.GetPublicTrends)
			pr.Get("/users/{handle}/skills", handlers.GetPublicSkills)
			pr.Get("/search", handlers.Search)
			pr.Get("/leaderboards", handlers.GetLeaderboards)
			pr.Get("/compare", handlers.CompareUsers)
		})

		// Protected /me Routes
		api.Route("/me", func(mr chi.Router) {
			mr.Use(mw.RequireAuth)

			// Onboarding & Profile
			mr.With(mw.RequireCSRF).Post("/onboarding", handlers.CompleteOnboarding)
			mr.Get("/profile", handlers.GetProfile)
			mr.With(mw.RequireCSRF).Patch("/profile", handlers.UpdateProfile)

			// Privacy
			mr.Get("/privacy", handlers.GetPrivacy)
			mr.With(mw.RequireCSRF).Patch("/privacy", handlers.UpdatePrivacy)
			mr.Get("/public-preview", handlers.GetPublicPreview)

			// Media / Avatar
			mr.With(mw.RequireCSRF).Post("/avatar-upload-intents", handlers.CreateAvatarIntent)
			mr.With(mw.RequireCSRF).Post("/avatar-upload-intents/{id}/complete", handlers.CompleteAvatarIntent)
			mr.With(mw.RequireCSRF).Delete("/avatar", handlers.ClearAvatar)

			// Analytics
			mr.Get("/summary", handlers.GetSummary)
			mr.Get("/trends/tokens", handlers.GetTokenTrends)
			mr.Get("/breakdowns/agents", handlers.GetAgentBreakdowns)
			mr.Get("/breakdowns/models", handlers.GetModelBreakdowns)
			mr.Get("/skills", handlers.GetSkills)
			mr.Get("/calendar", handlers.GetCalendar)
			mr.Get("/activity", handlers.GetActivity)
			mr.Get("/filter-options", handlers.GetFilterOptions)

			// Devices
			mr.Get("/devices", handlers.ListDevices)
			mr.With(mw.RequireCSRF).Post("/device-bindings", handlers.CreateDeviceBinding)
			mr.With(mw.RequireCSRF).Post("/device-grants", handlers.CreateDeviceGrant)
			mr.With(mw.RequireCSRF).Delete("/device-bindings/{id}", handlers.CancelDeviceBinding)
			mr.With(mw.RequireCSRF).Patch("/devices/{id}", handlers.UpdateDevice)
			mr.With(mw.RequireCSRF).Post("/devices/{id}/pause", handlers.PauseDevice)
			mr.With(mw.RequireCSRF).Post("/devices/{id}/resume", handlers.ResumeDevice)
			mr.With(mw.RequireCSRF).Delete("/devices/{id}", handlers.RevokeDevice)

			// Export
			mr.With(mw.RequireCSRF).Post("/exports", handlers.CreateExport)
			mr.Get("/exports", handlers.ListExports)
			mr.Get("/exports/{id}", handlers.GetExport)
			mr.Get("/exports/{id}/download", handlers.DownloadExport)

			// Deletion
			mr.With(mw.RequireCSRF).Post("/deletion-requests", handlers.RequestDeletion)
			mr.Get("/deletion-requests/{id}", handlers.GetDeletion)
			mr.With(mw.RequireCSRF).Post("/deletion-requests/{id}/cancel", handlers.CancelDeletion)
		})
	})

	return r
}
