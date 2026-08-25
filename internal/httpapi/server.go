package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/service"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	app *service.Service
	mux *http.ServeMux
}

func New(app *service.Service) *Server {
	s := &Server{app: app, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", s.handleStatic)
	s.mux.HandleFunc("/workbench", s.HandleWorkbench)
	s.mux.HandleFunc("/api/v1/tasks", s.HandleTasks)
	s.mux.HandleFunc("/api/v1/tasks/", s.HandleTaskRoutes)
	return s
}

func (s *Server) Handler() http.Handler { return logging(s.mux) }

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) HandleWorkbench(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.serveAsset(w, "web/index.html")
}
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.HandleWorkbench(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if !strings.HasPrefix(name, "web/") {
		http.NotFound(w, r)
		return
	}
	s.serveAsset(w, name)
}
func (s *Server) serveAsset(w http.ResponseWriter, name string) {
	data, err := webFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Write(data)
}

func (s *Server) HandleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := s.app.ListTasks(r.Context())
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	case http.MethodPost:
		var input service.CreateTaskInput
		if !s.decode(w, r, &input) {
			return
		}
		task, err := s.app.CreateTask(r.Context(), input)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, task)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) HandleTaskRoutes(w http.ResponseWriter, r *http.Request) {
	parts := taskRouteParts(r.URL.Path)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	taskID := parts[0]
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			aggregate, err := s.app.GetAggregate(r.Context(), taskID)
			if err != nil {
				s.writeError(w, err)
				return
			}
			s.writeJSON(w, http.StatusOK, aggregate)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "plan":
		s.HandlePlan(w, r, taskID)
	case "segments":
		s.HandleSegment(w, r, taskID)
	case "reworks":
		s.HandleRework(w, r, taskID)
	case "deviations":
		s.HandleDeviation(w, r, taskID)
	case "reviews":
		s.HandleReview(w, r, taskID)
	case "freeze":
		s.HandleFreeze(w, r, taskID)
	case "preflight":
		s.HandlePreflight(w, r, taskID)
	case "credential":
		s.HandleCredential(w, r, taskID)
	case "verify":
		s.HandleVerify(w, r, taskID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) HandlePlan(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method == http.MethodGet {
		if version := r.URL.Query().Get("planVersion"); version != "" {
			var parsed int64
			if _, err := fmt.Sscan(version, &parsed); err != nil {
				s.writeError(w, domain.Validation("planVersion", "方案版本无效"))
				return
			}
			value, err := s.app.PlanSnapshot(r.Context(), taskID, parsed)
			if err != nil {
				s.writeError(w, err)
				return
			}
			s.writeJSON(w, http.StatusOK, value)
			return
		}
		value, err := s.app.PlanSnapshots(r.Context(), taskID)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"snapshots": value})
		return
	}
	if r.Method == http.MethodPut {
		var input service.PublishPlanInput
		if !s.decode(w, r, &input) {
			return
		}
		value, err := s.app.PreviewPlan(r.Context(), taskID, input)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, value)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input service.PublishPlanInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PublishPlan(r.Context(), taskID, input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}
func (s *Server) HandleSegment(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method == http.MethodGet {
		value, err := s.app.ConstructionProgress(r.Context(), taskID, r.URL.Query().Get("result"))
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, value)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input service.ConstructionRequest
	if !s.decode(w, r, &input) {
		return
	}
	if input.SegmentID == "" {
		input.SegmentID = r.URL.Query().Get("segmentId")
	}
	value, err := s.app.RecordConstruction(r.Context(), taskID, input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) HandleRework(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input service.ReworkInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RecordRework(r.Context(), taskID, input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, value)
}
func (s *Server) HandleDeviation(w http.ResponseWriter, r *http.Request, taskID string) {
	switch r.Method {
	case http.MethodPost:
		var input service.CreateDeviationInput
		if !s.decode(w, r, &input) {
			return
		}
		value, err := s.app.CreateDeviation(r.Context(), taskID, input)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, value)
	case http.MethodPatch:
		var input service.CorrectionInput
		if !s.decode(w, r, &input) {
			return
		}
		value, err := s.app.CorrectDeviation(r.Context(), taskID, input)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (s *Server) HandleReview(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input service.ReviewInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviewDeviation(r.Context(), taskID, input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}
func (s *Server) HandleFreeze(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input service.FreezeInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.Freeze(r.Context(), taskID, input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}
func (s *Server) HandleCredential(w http.ResponseWriter, r *http.Request, taskID string) {
	switch r.Method {
	case http.MethodGet:
		c, err := s.app.Credential(r.Context(), taskID)
		if err != nil {
			s.writeError(w, err)
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", c.SerialNo))
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, c.PayloadJSON)
			return
		}
		s.writeJSON(w, http.StatusOK, c)
	case http.MethodPost:
		var input service.IssueInput
		if !s.decode(w, r, &input) {
			return
		}
		c, err := s.app.IssueCredential(r.Context(), taskID, input)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, c)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (s *Server) HandleVerify(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := s.app.VerifyCredentialDetailed(r.Context(), taskID)
	if err != nil && result.Valid == false {
		s.writeJSON(w, http.StatusOK, result)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) HandlePreflight(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	value, err := s.app.Preflight(r.Context(), taskID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoded := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		decoded <- decoder.Decode(target)
	}()
	if err := <-decoded; err != nil {
		s.writeError(w, domain.Validation("body", "请求 JSON 无效: %v", err))
		return false
	}
	return true
}
func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var de *domain.Error
	if errors.As(err, &de) {
		switch de.Kind {
		case domain.KindValidation:
			status = http.StatusBadRequest
		case domain.KindNotFound:
			status = http.StatusNotFound
		case domain.KindConflict:
			status = http.StatusConflict
		case domain.KindForbidden:
			status = http.StatusForbidden
		case domain.KindState:
			status = http.StatusUnprocessableEntity
		}
	}
	if de != nil {
		s.writeJSON(w, status, map[string]any{"error": de.Message, "kind": de.Kind, "field": de.Field, "currentVersion": de.Current, "matchedTaskCode": de.MatchedTaskCode, "blockingSegments": de.BlockingSegments})
		return
	}
	s.writeJSON(w, status, map[string]any{"error": err.Error(), "kind": domain.KindOf(err)})
}

func ActorFromHeaders(r *http.Request) domain.Actor {
	name := r.Header.Get("X-Actor-Name")
	role := domain.Role(r.Header.Get("X-Actor-Role"))
	if name == "" {
		name = "现场负责人"
	}
	if role == "" {
		role = domain.RoleManager
	}
	return domain.Actor{Name: name, Role: role}
}
func ParseTime(value string) time.Time { t, _ := time.Parse(time.RFC3339, value); return t }
