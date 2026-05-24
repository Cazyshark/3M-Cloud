package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/multi-ops/internal/master/api"
	"github.com/multi-ops/internal/master/audit"
	"github.com/multi-ops/internal/master/auth"
	"github.com/multi-ops/internal/master/middleware"
	"github.com/multi-ops/internal/master/store"
	"github.com/multi-ops/internal/master/ws"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	listen := getEnv("MASTER_LISTEN", ":8080")
	jwtSecret := getEnv("JWT_SECRET", "")
	adminPassword := getEnv("ADMIN_PASSWORD", "")

	// Require explicit credentials in production
	if jwtSecret == "" {
		log.Fatalln("[Master] JWT_SECRET must be set. Use a strong random string.")
	}
	if adminPassword == "" {
		log.Fatalln("[Master] ADMIN_PASSWORD must be set. Use a strong password.")
	}

	auditDir := getEnv("AUDIT_DIR", "/tmp/multi-ops-audit")
	ipWhitelistStr := getEnv("IP_WHITELIST", "")
	var ipWhitelistIPs []string
	if ipWhitelistStr != "" {
		ipWhitelistIPs = strings.Split(ipWhitelistStr, ",")
	}

	// Initialize core stores
	s := store.New()
	hub := ws.NewHub()
	gw := ws.NewGatewayConn()
	history := store.NewExecHistory(500)

	// Initialize auth
	users := auth.NewUserStore()
	jwtMgr := auth.NewJWTManager(jwtSecret, 24*time.Hour)

	// Create default admin user
	users.AddUser("admin", adminPassword, "", "admin")
	log.Printf("[Master] Admin user created")
	log.Printf("[Master] Run /api/setup-totp to enable TOTP for the admin account")

	// Initialize audit logger
	auditLog, err := audit.NewLogger(auditDir)
	if err != nil {
		log.Printf("[Master] Warning: audit log disabled: %v", err)
	}

	// Create API server
	server := api.New(s, hub, gw, history, users, jwtMgr, auditLog)

	// Build middleware chain
	rateLimiter := middleware.NewRateLimiter(100, time.Minute) // 100 req/min per IP
	loginRateLimiter := middleware.NewLoginRateLimiter()        // 5 login attempts/min per IP
	securityHeaders := middleware.SecurityHeaders
	ipWL := middleware.NewIPWhitelist(ipWhitelistIPs)
	auditMW := middleware.NewAuditLogger()
	requireAuth := middleware.RequireAuth(jwtMgr)

	mux := http.NewServeMux()

	// Public routes (no auth required)
	mux.Handle("/api/login", loginRateLimiter.Middleware(http.HandlerFunc(server.HandleLogin)))
	mux.HandleFunc("/api/status", server.HandleStatus)

	// WebSocket routes (auth via query param)
	mux.HandleFunc("/ws/dashboard", server.HandleDashboardWS)
	mux.HandleFunc("/ws/gateway", server.HandleGatewayWS)

	// Read-only API routes (accessible to admin and viewer)
	readOnly := http.NewServeMux()
	readOnly.HandleFunc("/api/machines", server.HandleMachines)
	readOnly.HandleFunc("/api/machine", server.HandleMachineDetail)
	readOnly.HandleFunc("/api/groups", server.HandleGroups)
	readOnly.HandleFunc("/api/machine/tags", server.HandleMachineTagsList)
	readOnly.HandleFunc("/api/exec/history", server.HandleExecHistory)
	readOnly.HandleFunc("/api/exec/detail", server.HandleExecDetail)
	readOnly.HandleFunc("/api/script-templates", server.HandleScriptTemplates)
	readOnly.HandleFunc("/api/audit", server.HandleAuditLog)

	// Write operations (admin only)
	writeOps := http.NewServeMux()
	writeOps.HandleFunc("/api/machine/tags", server.HandleMachineTags)
	writeOps.HandleFunc("/api/machine/group", server.HandleMachineGroup)
	writeOps.HandleFunc("/api/exec", server.HandleBatchExec)
	writeOps.HandleFunc("/api/file/upload", server.HandleFileUpload)
	writeOps.HandleFunc("/api/file/download", server.HandleFileDownload)
	writeOps.HandleFunc("/api/command", server.HandleCommand)
	writeOps.HandleFunc("/api/setup-totp", server.HandleSetupTOTP)

	// Mount read-only routes with auth middleware
	mux.Handle("/api/machines", requireAuth(readOnly))
	mux.Handle("/api/machine", requireAuth(readOnly))
	mux.Handle("/api/groups", requireAuth(readOnly))
	mux.Handle("/api/exec/history", requireAuth(readOnly))
	mux.Handle("/api/exec/detail", requireAuth(readOnly))
	mux.Handle("/api/script-templates", requireAuth(readOnly))
	mux.Handle("/api/audit", requireAuth(readOnly))

	// Mount write operations with role enforcement (admin only)
	mux.Handle("/api/machine/tags", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/machine/group", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/exec", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/file/upload", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/file/download", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/command", requireAuth(middleware.RequireRole("admin")(writeOps)))
	mux.Handle("/api/setup-totp", requireAuth(middleware.RequireRole("admin")(writeOps)))

	// Static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("Failed to create static FS: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// Apply global middleware chain: IP whitelist → rate limit → security headers → audit → router
	var handler http.Handler = mux
	handler = auditMW.Middleware(handler)
	handler = securityHeaders(handler)
	handler = rateLimiter.Middleware(handler)
	handler = ipWL.Middleware(handler)

	// Start stale machine cleanup
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		for range ticker.C {
			s.CleanupStale(90 * time.Second)
		}
	}()

	log.Printf("[Master] Starting server on %s", listen)
	go func() {
		if err := http.ListenAndServe(listen, handler); err != nil {
			log.Fatalf("[Master] Server error: %v", err)
		}
	}()

	fmt.Printf(`
  __  __       _ _   _ _____   _____
 |  \/  | __ _(_) | | |  ___| |  ___|__  _ __ ___ ___
 | |\/| |/ _` + "`" + ` | | | | | |_    | |_ / _ \| '__/ __/ _ \
 | |  | | (_| | | |_| |  _|   |  _| (_) | | | (_|  __/
 |_|  |_|\__,_|_|\___/|_|     |_|  \___/|_|  \___\___|

 Multi-Ops Master Server
 =======================
 Dashboard:    http://localhost%s
 API:          http://localhost%s/api/machines
 Gateway WS:   ws://localhost%s/ws/gateway
`, listen, listen, listen)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[Master] Shutting down...")
	if auditLog != nil {
		auditLog.Close()
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
