package dashboard

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/crypto-trading/trading/internal/config"
	"github.com/crypto-trading/trading/internal/marketdata"
	"github.com/crypto-trading/trading/internal/order"
	"github.com/crypto-trading/trading/internal/portfolio"
	"github.com/crypto-trading/trading/internal/risk"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Server struct {
	httpServer   *http.Server
	templates    map[string]*template.Template
	cfg          *config.Config
	riskMgr      *risk.Manager
	orderMgr     *order.Manager
	portfolioMgr *portfolio.Manager
	mdService    *marketdata.Service
	startTime    time.Time
	logger       *slog.Logger
}

func New(
	addr string,
	cfg *config.Config,
	riskMgr *risk.Manager,
	orderMgr *order.Manager,
	portfolioMgr *portfolio.Manager,
	mdService *marketdata.Service,
	logger *slog.Logger,
) *Server {
	s := &Server{
		cfg:          cfg,
		riskMgr:      riskMgr,
		orderMgr:     orderMgr,
		portfolioMgr: portfolioMgr,
		mdService:    mdService,
		startTime:    time.Now(),
		logger:       logger,
	}

	s.templates = s.parseTemplates()

	mux := http.NewServeMux()
	s.routes(mux)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s
}

func (s *Server) ListenAndServe() error {
	s.logger.Info("dashboard server starting", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleOverview)
	mux.HandleFunc("GET /positions", s.handlePositions)
	mux.HandleFunc("GET /orders", s.handleOrders)
	mux.HandleFunc("GET /risk", s.handleRisk)

	mux.HandleFunc("GET /fragments/overview", s.handleOverviewFragment)
	mux.HandleFunc("GET /fragments/positions", s.handlePositionsFragment)
	mux.HandleFunc("GET /fragments/orders", s.handleOrdersFragment)
	mux.HandleFunc("GET /fragments/risk", s.handleRiskFragment)

	mux.HandleFunc("POST /api/killswitch/activate", s.handleKillSwitchActivate)
	mux.HandleFunc("POST /api/killswitch/deactivate", s.handleKillSwitchDeactivate)
}

func (s *Server) parseTemplates() map[string]*template.Template {
	funcMap := template.FuncMap{
		"riskModeColor": riskModeColor,
		"statusColor":   orderStatusColor,
		"modeColor":     tradingModeColor,
		"pctWidth":      pctWidth,
	}

	pages := []string{"overview", "positions", "orders", "risk"}
	templates := make(map[string]*template.Template, len(pages))

	for _, page := range pages {
		t := template.Must(
			template.New("").Funcs(funcMap).ParseFS(
				templatesFS,
				"templates/base.html",
				fmt.Sprintf("templates/%s.html", page),
			),
		)
		templates[page] = t
	}

	return templates
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := s.templates[page]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		s.logger.Error("template render error", "page", page, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) renderFragment(w http.ResponseWriter, page, fragment string, data any) {
	tmpl, ok := s.templates[page]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, fragment, data); err != nil {
		s.logger.Error("fragment render error", "page", page, "fragment", fragment, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func riskModeColor(mode string) string {
	switch mode {
	case "NORMAL":
		return "text-emerald-400 bg-emerald-400/10 ring-emerald-400/30"
	case "WARNING":
		return "text-amber-400 bg-amber-400/10 ring-amber-400/30"
	case "DEGRADED":
		return "text-orange-400 bg-orange-400/10 ring-orange-400/30"
	case "DATA_STALE":
		return "text-yellow-400 bg-yellow-400/10 ring-yellow-400/30"
	case "HALTED":
		return "text-rose-400 bg-rose-400/10 ring-rose-400/30"
	default:
		return "text-slate-400 bg-slate-400/10 ring-slate-400/30"
	}
}

func orderStatusColor(status string) string {
	switch status {
	case "FILLED":
		return "text-emerald-400"
	case "PARTIAL_FILL":
		return "text-sky-400"
	case "CANCELLED", "REJECTED", "SUBMIT_FAILED":
		return "text-rose-400"
	case "ACKNOWLEDGED", "SUBMITTED":
		return "text-amber-400"
	case "PENDING_NEW":
		return "text-slate-400"
	default:
		return "text-slate-400"
	}
}

func tradingModeColor(mode string) string {
	switch mode {
	case "live":
		return "text-rose-400 bg-rose-400/10 ring-rose-400/30"
	case "dry_run":
		return "text-sky-400 bg-sky-400/10 ring-sky-400/30"
	case "backtest":
		return "text-violet-400 bg-violet-400/10 ring-violet-400/30"
	default:
		return "text-slate-400 bg-slate-400/10 ring-slate-400/30"
	}
}

func pctWidth(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%.1f%%", pct)
}
