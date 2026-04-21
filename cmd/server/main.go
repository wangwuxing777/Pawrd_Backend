package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/handlers"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/merchant"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/places"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

var port = "8000"

func init() {
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
}

func bookingReconcileLoopConfig() (bool, time.Duration, int, string, bool) {
	secret := strings.TrimSpace(os.Getenv("BOOKING_SYNC_SHARED_SECRET"))
	if secret == "" {
		return false, 0, 0, "", false
	}
	rawInterval := strings.TrimSpace(os.Getenv("BOOKING_RECONCILE_INTERVAL_SECONDS"))
	if rawInterval == "" {
		return false, 0, 0, "", false
	}
	seconds, err := strconv.Atoi(rawInterval)
	if err != nil || seconds <= 0 {
		return false, 0, 0, "", false
	}
	limit := 50
	if rawLimit := strings.TrimSpace(os.Getenv("BOOKING_RECONCILE_LIMIT")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	syncState := strings.TrimSpace(strings.ToLower(os.Getenv("BOOKING_RECONCILE_SYNC_STATE")))
	if syncState != "" && syncState != "stale" && syncState != "never_synced" && syncState != "sync_error" {
		syncState = ""
	}
	force := strings.EqualFold(strings.TrimSpace(os.Getenv("BOOKING_RECONCILE_FORCE")), "true")
	return true, time.Duration(seconds) * time.Second, limit, syncState, force
}

func bookingFreshnessWindowConfig() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BOOKING_MIRROR_FRESHNESS_SECONDS"))
	if raw == "" {
		return 2 * time.Minute
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 2 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}

func startBookingReconcileLoop(listenPort string) {
	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if !enabled {
		return
	}
	secret := strings.TrimSpace(os.Getenv("BOOKING_SYNC_SHARED_SECRET"))
	query := fmt.Sprintf("limit=%d", limit)
	if syncState != "" {
		query += "&sync_state=" + syncState
	}
	if force {
		query += "&force=true"
	}
	url := fmt.Sprintf("http://127.0.0.1:%s/api/bookings/reconcile-stale?%s", listenPort, query)
	go func() {
		log.Printf("Booking reconcile loop enabled: interval=%s limit=%d sync_state=%s force=%v", interval, limit, syncState, force)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		client := &http.Client{Timeout: 30 * time.Second}
		for range ticker.C {
			req, err := http.NewRequest(http.MethodPost, url, nil)
			if err != nil {
				log.Printf("Booking reconcile loop request build failed: %v", err)
				continue
			}
			req.Header.Set("X-Booking-Sync-Token", secret)
			req.Header.Set("X-Request-ID", "booking-reconcile-runner")
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Booking reconcile loop request failed: %v", err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 300 {
				log.Printf("Booking reconcile loop non-2xx status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}
	}()
}

func main() {
	// Load Configuration
	cfg := config.LoadConfig()
	merchantVaccinationClient := merchant.NewClient(cfg)
	handlers.SetMirrorFreshnessWindow(bookingFreshnessWindowConfig())

	// Initialize chat session store (30-minute TTL)
	sessionStore := chat.NewSessionStore(30 * time.Minute)

	// Parse flags for seeding DB
	seedDB := flag.Bool("seed", false, "Seed the database with initial scenario data")
	flag.Parse()

	// Initialize DB
	db, err := models.InitDB(cfg)
	if err != nil {
		log.Fatalf("Fatal error initializing simple db: %v", err)
	}

	// Initialize auth database
	if err := models.InitAuthDB(); err != nil {
		log.Fatalf("Fatal error initializing auth db: %v", err)
	}

	if *seedDB {
		SeedDatabase(db)
		fmt.Println("Seeding complete. Exiting...")
		return
	}

	ragClient := rag.NewClient(cfg, db)
	if cfg.HKInsuranceRAGEnabled && cfg.HKInsuranceRAGRebuildOnStart {
		if err := ragClient.Rebuild(context.Background()); err != nil {
			log.Printf("HK insurance RAG rebuild on start failed: %v", err)
		} else {
			log.Printf("HK insurance RAG rebuild on start completed")
		}
	}

	// Initialize new Gin router for scenarios API
	insuranceV1Router := handlers.NewInsuranceV1Handler(db)

	// Create a new mux
	mux := http.NewServeMux()

	// Media upload + static file serving
	uploadsDir := "assets/uploads"
	_ = os.MkdirAll(uploadsDir, 0755)
	mux.HandleFunc("/media/upload", handlers.NewMediaUploadHandler("http://localhost:8000"))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))

	// Core handlers
	mux.HandleFunc("/vaccines", handlers.VaccinesHandler)
	mux.HandleFunc("/register", handlers.RegisterHandler)
	mux.HandleFunc("/posts", handlers.NewPostsHandler(db))
	mux.HandleFunc("/posts/{id}", handlers.NewPostDetailHandler(db))
	mux.HandleFunc("/posts/{id}/like", handlers.NewPostLikeHandler(db))
	mux.HandleFunc("/posts/{id}/comments", handlers.NewPostCommentsHandler(db))
	mux.HandleFunc("/posts/{id}/comments/{commentId}", handlers.NewCommentDeleteHandler(db))

	// Seed test accounts on every startup (idempotent — skips existing)
	SeedTestAccounts()

	// Auth endpoints
	mux.HandleFunc("/api/auth/login", handlers.NewAuthLoginHandler())
	mux.HandleFunc("/api/auth/register", handlers.NewAuthRegisterHandler())
	mux.HandleFunc("/api/bookings", handlers.NewAppBookingsHandler(db, merchantVaccinationClient))
	mux.HandleFunc("/api/bookings/{bookingID}", handlers.NewAppBookingDetailHandler(db, merchantVaccinationClient))
	mux.HandleFunc("/api/bookings/sync", handlers.NewAppBookingSyncHandler(db, os.Getenv("BOOKING_SYNC_SHARED_SECRET")))
	mux.HandleFunc("/api/bookings/reconcile-stale", handlers.NewAppBookingReconcileHandler(db, merchantVaccinationClient, os.Getenv("BOOKING_SYNC_SHARED_SECRET")))
	mux.HandleFunc("/clinics", handlers.NewClinicsHandler(cfg))
	mux.HandleFunc("/emergency-clinics", handlers.NewEmergencyClinicsHandler(cfg))

	// Insurance handlers
	mux.HandleFunc("/insurance-companies", handlers.InsuranceCompaniesHandler)
	mux.HandleFunc("/insurance-products", handlers.InsuranceProductsHandler)
	mux.HandleFunc("/coverage-list", handlers.CoverageListHandler)
	mux.HandleFunc("/coverage-limits", handlers.CoverageLimitsHandler)
	mux.HandleFunc("/sub-coverage-limits", handlers.SubCoverageLimitsHandler)

	// Legacy handlers
	mux.HandleFunc("/insurance-providers", handlers.InsuranceProvidersHandler)
	mux.HandleFunc("/service-subcategories", handlers.ServiceSubcategoriesHandler)

	// Legacy RAG chat handler (now with session support)
	mux.HandleFunc("/api/chat", handlers.NewRAGHandler(ragClient, sessionStore))

	// New chat session endpoints
	mux.HandleFunc("/rag-test", handlers.NewRAGTestPageHandler())
	mux.HandleFunc("/api/chat/session", handlers.NewChatSessionHandler(sessionStore))
	mux.HandleFunc("/api/chat/session/", handlers.NewChatSelectProviderHandler(sessionStore)) // matches /api/chat/session/{id}/provider
	mux.HandleFunc("/api/chat/providers", handlers.NewChatProvidersHandler(ragClient))
	mux.HandleFunc("/api/chat/ask", handlers.NewChatAskHandler(sessionStore, ragClient))

	// Vets handler
	placesClient := places.NewClient(cfg.MapsAPIKey)
	mux.HandleFunc("/api/vets", handlers.NewVetsHandler(placesClient))

	// Shop handlers
	mux.HandleFunc("/api/shop/products", handlers.NewShopHandler(cfg))
	mux.HandleFunc("/api/shop/products/{handle}", handlers.NewShopProductDetailHandler(cfg))
	mux.HandleFunc("/api/shop/categories", handlers.NewShopCategoriesHandler(cfg))
	mux.HandleFunc("/api/shop/search", handlers.NewShopSearchHandler(cfg))
	mux.HandleFunc("/api/shop/checkout/payment-sheet", handlers.NewShopPaymentSheetHandler(cfg))

	// Blog handlers
	mux.HandleFunc("/api/posts", handlers.NewBlogHandler(db))

	// Medical services handlers (public + admin)
	mux.HandleFunc("/api/medical/services", handlers.NewMedicalServicesHandler(db))
	mux.HandleFunc("/api/medical/services/{category}", handlers.NewMedicalServiceDetailHandler(db))
	mux.HandleFunc("/api/medical/admin/services/{id}", handlers.NewMedicalAdminUpdateHandler(db))
	mux.HandleFunc("/api/medical/vaccinations/availability", handlers.NewVaccinationAvailabilityProxyHandler(merchantVaccinationClient))
	mux.HandleFunc("/api/medical/vaccinations/bookings", handlers.NewVaccinationBookingCreateProxyHandler(merchantVaccinationClient))
	mux.HandleFunc("/api/medical/vaccinations/bookings/{externalBookingID}", handlers.NewVaccinationBookingGetProxyHandler(merchantVaccinationClient))
	mux.HandleFunc("/api/medical/vaccinations/bookings/{externalBookingID}/cancel", handlers.NewVaccinationBookingCancelProxyHandler(merchantVaccinationClient))

	// Health report extraction + fusion handlers (profile backend pipeline)
	mux.HandleFunc("/api/profile/storage/cos/presign-upload", handlers.NewCOSPresignUploadHandler())
	mux.HandleFunc("/api/profile/health-reports", handlers.NewHealthReportCreateHandler(db))
	mux.HandleFunc("/api/profile/health-reports/{id}", handlers.NewHealthReportDetailHandler(db))
	mux.HandleFunc("/api/profile/health-reports/observations/{observationId}/review", handlers.NewObservationReviewHandler(db))
	mux.HandleFunc("/api/profile/pets/{petId}/health", handlers.NewPetHealthProfileHandler(db))

	// Partner application handlers
	mux.HandleFunc("/api/partners/apply", handlers.NewPartnersApplyHandler(db))
	mux.HandleFunc("/api/admin/partners", handlers.NewPartnersAdminListHandler(db))
	partnerAction := handlers.NewPartnersAdminActionHandler(db)
	mux.HandleFunc("/api/admin/partners/{id}/approve", partnerAction)
	mux.HandleFunc("/api/admin/partners/{id}/reject", partnerAction)

	// Seed medical demo content on startup (idempotent)
	SeedMedicalServices(db)

	// Auto-seed scenario data if the table is empty (idempotent)
	var scenarioCount int64
	db.Model(&models.Scenario{}).Count(&scenarioCount)
	if scenarioCount == 0 {
		log.Println("No scenarios found, running auto-seed...")
		SeedDatabase(db)
	}

	// Mount Gin engine onto standard mux
	// We handle both /api/v1 and /api/v1/ to be safe
	v1Handler := http.StripPrefix("/api/v1", insuranceV1Router)
	mux.Handle("/api/v1", v1Handler)
	mux.Handle("/api/v1/", v1Handler)

	startBookingReconcileLoop(port)

	fmt.Printf("PetWell Backend running at http://localhost:%s\n", port)
	fmt.Println("Chat endpoints:")
	fmt.Println("  POST /api/chat/session          - Create session")
	fmt.Println("  POST /api/chat/session/{id}/provider - Select provider")
	fmt.Println("  GET  /api/chat/providers         - List providers")
	fmt.Println("  POST /api/chat/ask               - Ask with context")

	err = http.ListenAndServe(":"+port, mux)
	if err != nil {
		fmt.Printf("Server failed to start: %v\n", err)
	}
}
