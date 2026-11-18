package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
)

type plan struct {
	TaskID   int64            `json:"task_id"`
	Findings []captureRequest `json:"findings"`
}

type captureRequest struct {
	FindingID            int64             `json:"finding_id"`
	RunDir               string            `json:"run_dir"`
	Mode                 string            `json:"mode"`
	PageURL              string            `json:"page_url"`
	PreconditionSelector string            `json:"precondition_selector"`
	ActionSelector       string            `json:"action_selector"`
	WaitSelector         string            `json:"wait_selector"`
	WaitText             string            `json:"wait_text"`
	PrepareJS            string            `json:"prepare_js"`
	RouteMatch           string            `json:"route_match"`
	HTTPMethod           string            `json:"http_method"`
	RequestBody          string            `json:"request_body"`
	ContentType          string            `json:"content_type"`
	Query                map[string]string `json:"query"`
	Headers              map[string]string `json:"headers"`
	TrafficID            string            `json:"traffic_id"`
	Title                string            `json:"title"`
	Note                 string            `json:"note"`
	SourceURL            string            `json:"source_url"`
	FullPage             bool              `json:"full_page"`
}

func main() {
	var (
		planPath = flag.String("plan", "", "backfill plan JSON")
		dataDir  = flag.String("data-dir", envFirst("GHOUL_DATA_DIR", "ARTEX_DATA_DIR"), "runtime data directory")
		dryRun   = flag.Bool("dry-run", false, "validate findings and print actions without writing evidence")
	)
	flag.Parse()
	if strings.TrimSpace(*planPath) == "" {
		fatalf("-plan is required")
	}
	if strings.TrimSpace(*dataDir) == "" {
		fatalf("GHOUL_DATA_DIR or -data-dir is required")
	}
	dsn := envFirst("GHOUL_PG_DSN", "ARTEX_PG_DSN")
	if dsn == "" {
		fatalf("GHOUL_PG_DSN or ARTEX_PG_DSN is required")
	}
	raw, err := os.ReadFile(*planPath)
	if err != nil {
		fatalf("read plan: %v", err)
	}
	var p plan
	if err = json.Unmarshal(raw, &p); err != nil {
		fatalf("parse plan: %v", err)
	}
	if p.TaskID <= 0 || len(p.Findings) == 0 {
		fatalf("plan requires task_id and at least one finding")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer pg.Close()
	store := evidence.New(pg, nil, filepath.Join(*dataDir, "evidence"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	results := make([]map[string]any, 0, len(p.Findings))
	for _, req := range p.Findings {
		finding, err := pg.GetFinding(req.FindingID)
		if err != nil {
			fatalf("finding %d: %v", req.FindingID, err)
		}
		if finding == nil || finding.TaskID == nil || *finding.TaskID != p.TaskID {
			fatalf("finding %d does not belong to task %d", req.FindingID, p.TaskID)
		}
		exists, err := hasBackfillEvidence(ctx, pg, req)
		if err != nil {
			fatalf("finding %d existing evidence: %v", req.FindingID, err)
		}
		if exists {
			results = append(results, map[string]any{"finding_id": req.FindingID, "status": "skipped", "reason": "same capture already exists"})
			continue
		}
		if *dryRun {
			results = append(results, map[string]any{"finding_id": req.FindingID, "status": "dry_run", "mode": req.Mode, "run_dir": req.RunDir})
			continue
		}
		item, err := store.CaptureFindingScreenshot(ctx, db.FindingScreenshotCaptureInput{
			TaskID: p.TaskID, FindingID: req.FindingID, RunDir: req.RunDir, Mode: req.Mode,
			PageURL: req.PageURL, PreconditionSelector: req.PreconditionSelector,
			ActionSelector: req.ActionSelector, WaitSelector: req.WaitSelector,
			WaitText: req.WaitText, PrepareJS: req.PrepareJS, RouteMatch: req.RouteMatch,
			HTTPMethod: req.HTTPMethod, RequestBody: req.RequestBody, ContentType: req.ContentType,
			Query: req.Query, Headers: req.Headers, TrafficID: req.TrafficID,
			Title: req.Title, Note: req.Note, SourceURL: req.SourceURL, FullPage: req.FullPage,
		})
		if err != nil {
			fatalf("capture finding %d: %v", req.FindingID, err)
		}
		results = append(results, map[string]any{"finding_id": req.FindingID, "status": "captured", "evidence_id": item.ID, "mode": req.Mode})
	}
	out, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(out))
}

func hasBackfillEvidence(ctx context.Context, pg *db.DB, req captureRequest) (bool, error) {
	items, err := pg.ListFindingEvidence(ctx, req.FindingID)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		var meta struct {
			CaptureMode string `json:"capture_mode"`
		}
		if json.Unmarshal(item.MetadataJSON, &meta) != nil {
			continue
		}
		if meta.CaptureMode == req.Mode {
			return true, nil
		}
	}
	return false, nil
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
