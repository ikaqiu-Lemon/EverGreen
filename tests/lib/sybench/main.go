package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const (
	sourceID        core.LogicalID = "s-perf-source"
	noteID          core.LogicalID = "n-perf-note"
	notePath                       = "box/perf-note.sy"
	noteSegmentID   core.LogicalID = "seg-perf-source"
	noteCandidate   core.LogicalID = "cand-perf-knowledge"
	noteParagraphID                = "20261013000002-noteseg"
)

type metricResult struct {
	Schema        string  `json:"schema"`
	Metric        string  `json:"metric"`
	Claims        int     `json:"claims"`
	Relations     int     `json:"relations"`
	Warmup        int     `json:"warmup"`
	Samples       int     `json:"samples"`
	P50MS         float64 `json:"p50_ms"`
	P95MS         float64 `json:"p95_ms"`
	P99MS         float64 `json:"p99_ms"`
	MaxMS         float64 `json:"max_ms"`
	MaxRSSBytes   uint64  `json:"max_rss_bytes"`
	HeapAlloc     uint64  `json:"heap_alloc_bytes"`
	HeapSys       uint64  `json:"heap_sys_bytes"`
	GoVersion     string  `json:"go_version"`
	GOOS          string  `json:"goos"`
	GOARCH        string  `json:"goarch"`
	LogicalCPU    int     `json:"logical_cpus"`
	CommitBefore  int     `json:"commits_before,omitempty"`
	CommitAfter   int     `json:"commits_after,omitempty"`
	ObservedCount int     `json:"observed_count,omitempty"`
}

type corpusResult struct {
	Schema    string `json:"schema"`
	Root      string `json:"root"`
	Claims    int    `json:"claims"`
	Relations int    `json:"relations"`
	Locations int    `json:"locations"`
	SYFiles   int    `json:"sy_files"`
	SYBytes   int64  `json:"sy_bytes"`
	Digest    string `json:"digest"`
}

type gitResult struct {
	Schema  string `json:"schema"`
	Head    string `json:"head"`
	Commits int    `json:"commits"`
}

func main() {
	mode := flag.String("mode", "", "prepare, inspect, init-git, or a metric name")
	root := flag.String("root", "", "benchmark workspace root")
	claims := flag.Int("claims", 10000, "expected/generated Claim count")
	relations := flag.Int("relations", 30000, "expected/generated relation count")
	warmup := flag.Int("warmup", 3, "discarded warmup operations")
	samples := flag.Int("samples", 50, "recorded operations")
	flag.Parse()

	if err := run(*mode, *root, *claims, *relations, *warmup, *samples); err != nil {
		log.Fatalf("sy_bench: %v", err)
	}
}

func run(mode, root string, claims, relations, warmup, samples int) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("-root is required")
	}
	if claims <= 1 || relations < 0 || relations > claims*(claims-1) {
		return fmt.Errorf("invalid corpus size: claims=%d relations=%d", claims, relations)
	}
	switch mode {
	case "prepare":
		result, err := prepareCorpus(root, claims, relations)
		return emit(result, err)
	case "inspect":
		result, err := inspectCorpus(root)
		return emit(result, err)
	case "init-git":
		result, err := initGit(root)
		return emit(result, err)
	case "startup_scan", "claims_query", "relation_query", "note_edit", "plan_apply", "index_rebuild":
		if warmup < 0 || samples <= 0 {
			return fmt.Errorf("invalid sampling: warmup=%d samples=%d", warmup, samples)
		}
		result, err := benchmark(mode, root, claims, relations, warmup, samples)
		return emit(result, err)
	default:
		return fmt.Errorf("unknown -mode %q", mode)
	}
}

func emit(value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func prepareCorpus(root string, claims, relations int) (corpusResult, error) {
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return corpusResult{}, fmt.Errorf("refusing to replace existing corpus root %s", root)
		}
		return corpusResult{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return corpusResult{}, err
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".siyuan/\n"), 0o644); err != nil {
		return corpusResult{}, err
	}
	registry := core.DefaultRegistry()
	host, err := core.NewFilesystemAuthority(core.FilesystemAuthorityOptions{Root: root, Registry: registry})
	if err != nil {
		return corpusResult{}, err
	}
	service := core.NewAuthorityService(host, core.CommandPolicy{}, registry)
	principal := core.Principal{
		Type: core.PrincipalAgent, ID: "perf-generator",
		AuthSource: "sy-perf", RequestReason: "deterministic performance corpus",
	}

	source, err := service.PlanSourceCapture(context.Background(), principal, core.SourceCaptureRequest{
		OperationID: "perf-source", LogicalID: sourceID,
		DocumentID: "20261013000000-perfsrc", Path: "box/perf-source.sy",
		URL: "https://example.com/evergreen/performance", Title: "Evergreen performance source",
		Body:   "Deterministic source material for Evergreen performance measurement.",
		Reason: "Performance corpus", Domain: "performance", Tags: []string{"performance"},
		CapturedAt: "2026-10-13T00:00:00Z",
	})
	if err != nil || source.Operation == nil {
		return corpusResult{}, fmt.Errorf("plan Source fixture: %w", err)
	}
	if err = writeImage(root, source.Operation.Images[0]); err != nil {
		return corpusResult{}, err
	}

	note, err := service.PlanNoteCreate(context.Background(), principal, core.NoteCreateRequest{
		OperationID: "perf-note", LogicalID: noteID,
		DocumentID: "20261013000001-perfnot", Path: notePath,
		SourceID: sourceID, Title: "Evergreen performance Note",
		Segments: []core.NoteCreateSegment{{
			SegmentID: noteSegmentID, Role: "note_segment",
			Locator: &core.Locator{Kind: "source-lines", Value: "L1-L1"},
			Content: json.RawMessage(fmt.Sprintf(
				`[{"ID":%q,"Type":"NodeParagraph","Data":"Performance source paragraph."}]`,
				noteParagraphID)),
		}},
		Candidates: []core.NoteCreateCandidate{{
			CandidateID: noteCandidate, ClaimKind: core.KnowledgeKind,
			Title: "Performance candidate", LogicalSlug: "performance-candidate",
			SegmentRefs: []core.LogicalID{noteSegmentID},
			Payload: json.RawMessage(
				`[{"ID":"20261013000003-candpay","Type":"NodeParagraph","Data":"Performance candidate payload."}]`),
			Relation: "support", Reason: "The source segment supports this candidate.",
		}},
		Coverage: []core.CoverageModule{{
			ModuleID: "coverage-perf", Disposition: "candidate",
			SegmentRefs: []core.LogicalID{noteSegmentID}, CandidateID: noteCandidate,
		}},
	})
	if err != nil {
		return corpusResult{}, fmt.Errorf("plan Note fixture: %w", err)
	}
	if err = writeImage(root, note.Operation.Images[0]); err != nil {
		return corpusResult{}, err
	}

	edgeCounts := distribute(relations, claims)
	for index := 0; index < claims; index++ {
		raw, buildErr := claimDocument(index, claims, edgeCounts[index], registry)
		if buildErr != nil {
			return corpusResult{}, buildErr
		}
		path := filepath.Join(root, "claims", fmt.Sprintf("%03d", index/100), claimID(index)+".sy")
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return corpusResult{}, err
		}
		if err = os.WriteFile(path, raw, 0o644); err != nil {
			return corpusResult{}, err
		}
	}
	return inspectCorpus(root)
}

func writeImage(root string, image core.AfterImage) error {
	path := filepath.Join(root, filepath.FromSlash(image.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, image.Bytes, 0o644)
}

func distribute(total, items int) []int {
	counts := make([]int, items)
	base, extra := total/items, total%items
	for index := range counts {
		counts[index] = base
		if index < extra {
			counts[index]++
		}
	}
	return counts
}

func claimDocument(index, claims, relationCount int, registry *core.Registry) ([]byte, error) {
	edges := make([]core.TypedEdge, 0, relationCount)
	for edgeIndex := 0; edgeIndex < relationCount; edgeIndex++ {
		target := (index + edgeIndex + 1) % claims
		edges = append(edges, core.TypedEdge{
			ID:     core.EdgeID(fmt.Sprintf("edge-perf-%05d-%05d", index, edgeIndex)),
			Schema: core.ArgumentSchema,
			Target: core.EntityRef{EntityType: core.EntityClaim, LogicalID: core.LogicalID(claimID(target))},
			Type:   "supports", Reason: fmt.Sprintf("Deterministic support %05d/%05d.", index, edgeIndex),
			CreatedAt: "2026-10-13T00:00:00Z", UpdatedAt: "2026-10-13T00:00:00Z",
		})
	}
	base := []byte(fmt.Sprintf(
		`{"ID":%q,"Type":"NodeDocument","Spec":"5","Properties":{"title":%q},`+
			`"Children":[{"ID":%q,"Type":"NodeParagraph","Data":%q}]}`,
		physicalID(index), fmt.Sprintf("Claim %05d", index), physicalID(index+claims),
		fmt.Sprintf("Deterministic Claim body %05d with searchable performance content.", index),
	))
	return core.EncodeSY(base, &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{
			LogicalID: core.LogicalID(claimID(index)), EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &core.Claim{
			ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", Tags: []string{"performance"}, KindData: json.RawMessage(`{}`),
		},
		Relations: core.Relations{Outgoing: edges},
	}, registry)
}

func claimID(index int) string {
	return fmt.Sprintf("c-perf-%05d", index)
}

func physicalID(index int) string {
	return fmt.Sprintf("20261013000000-%07x", index)
}

func inspectCorpus(root string) (corpusResult, error) {
	index, err := core.ScanFS(os.DirFS(root), core.DefaultRegistry())
	if err != nil {
		return corpusResult{}, err
	}
	result := corpusResult{
		Schema: "evergreen.sy-perf-corpus/v1", Root: filepath.Base(root),
		Locations: len(index.Locations),
	}
	hasher := sha256.New()
	var paths []string
	err = fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(path), ".sy") {
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		return corpusResult{}, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return corpusResult{}, readErr
		}
		fmt.Fprintf(hasher, "%s\x00", path)
		_, _ = hasher.Write(raw)
		result.SYFiles++
		result.SYBytes += int64(len(raw))
		document, decodeErr := core.DecodeSY(raw, core.DefaultRegistry())
		if decodeErr != nil {
			return corpusResult{}, decodeErr
		}
		if document.Envelope != nil && document.Envelope.Claim != nil {
			result.Claims++
			result.Relations += len(document.Envelope.Relations.Outgoing)
		}
	}
	result.Digest = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	return result, nil
}

func initGit(root string) (gitResult, error) {
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "sy-perf@example.invalid"},
		{"config", "user.name", "Evergreen SY Performance"},
		{"add", ".gitignore", "box", "claims"},
		{"commit", "-q", "-m", "Seed deterministic .sy performance corpus"},
	} {
		if _, err := gitOutput(root, args...); err != nil {
			return gitResult{}, err
		}
	}
	head, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return gitResult{}, err
	}
	commits, err := gitCommitCount(root)
	return gitResult{
		Schema: "evergreen.sy-perf-git/v1", Head: strings.TrimSpace(head), Commits: commits,
	}, err
}

func gitOutput(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

func gitCommitCount(root string) (int, error) {
	output, err := gitOutput(root, "rev-list", "--count", "HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(output))
}

func benchmark(
	metric, root string,
	claims, relations, warmup, samples int,
) (metricResult, error) {
	registry := core.DefaultRegistry()
	ctx := context.Background()
	var operation func(int) (int, error)
	result := metricResult{
		Schema: "evergreen.sy-perf-result/v1", Metric: metric,
		Claims: claims, Relations: relations, Warmup: warmup, Samples: samples,
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		LogicalCPU: runtime.NumCPU(),
	}

	switch metric {
	case "startup_scan":
		operation = func(_ int) (int, error) {
			index, err := core.ScanFS(os.DirFS(root), registry)
			if err != nil {
				return 0, err
			}
			return len(index.Locations), nil
		}
	case "claims_query":
		repository, err := core.NewSYReadRepository(root, registry)
		if err != nil {
			return result, err
		}
		provider := core.NewClaimsAVProvider(repository, nil, registry)
		operation = func(_ int) (int, error) {
			page, queryErr := provider.Query(ctx, core.ClaimsViewID, core.AVQuery{Limit: 100})
			if queryErr != nil {
				return 0, queryErr
			}
			if page.Total != claims || len(page.Rows) != 100 {
				return 0, fmt.Errorf("Claims query returned total=%d rows=%d", page.Total, len(page.Rows))
			}
			return page.Total, nil
		}
	case "relation_query":
		repository, err := core.NewSYReadRepository(root, registry)
		if err != nil {
			return result, err
		}
		operation = func(_ int) (int, error) {
			edges, queryErr := core.RelationsWorkspace(
				ctx, repository, registry, core.LogicalID(claimID(claims/2)))
			if queryErr != nil {
				return 0, queryErr
			}
			if len(edges) == 0 {
				return 0, errors.New("relation projection returned no edges")
			}
			return len(edges), nil
		}
	case "note_edit":
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(notePath)))
		if err != nil {
			return result, err
		}
		edited, err := editNodeData(raw, noteParagraphID, "Edited performance source paragraph.")
		if err != nil {
			return result, err
		}
		operation = func(_ int) (int, error) {
			normalized, normalizeErr := core.NormalizeNoteReviewEdit(raw, edited, nil, registry)
			if normalizeErr != nil {
				return 0, normalizeErr
			}
			if normalized.Snapshot.NoteID != noteID {
				return 0, fmt.Errorf("normalized Note ID = %q", normalized.Snapshot.NoteID)
			}
			return len(normalized.After), nil
		}
	case "plan_apply":
		committer, err := core.NewLocalGitCommitter(root)
		if err != nil {
			return result, err
		}
		host, err := core.NewFilesystemAuthority(core.FilesystemAuthorityOptions{
			Root: root, Registry: registry, Committer: committer,
		})
		if err != nil {
			return result, err
		}
		service := core.NewAuthorityService(host, core.CommandPolicy{}, registry)
		principal := core.Principal{Type: core.PrincipalUser, ID: "perf-owner", AuthSource: "sy-perf"}
		snapshot, err := service.InspectClaim(ctx, core.LogicalID(claimID(0)))
		if err != nil {
			return result, err
		}
		base := snapshot.Base
		result.CommitBefore, err = gitCommitCount(root)
		if err != nil {
			return result, err
		}
		operation = func(iteration int) (int, error) {
			tag := "performance-even"
			if iteration%2 != 0 {
				tag = "performance-odd"
			}
			tags := []string{tag}
			planned, planErr := service.PlanClaimEdit(ctx, principal, core.ClaimEditRequest{
				OperationID: fmt.Sprintf("sy-perf-plan-apply-%04d", iteration+warmup),
				LogicalID:   core.LogicalID(claimID(0)), Base: base,
				Edit: core.ClaimEdit{Tags: &tags},
			})
			if planErr != nil {
				return 0, planErr
			}
			record, applyErr := service.Apply(ctx, principal, core.ApplyRequest{Operation: planned})
			if applyErr != nil {
				return 0, applyErr
			}
			if record.State != core.OperationCompleted || record.GitCommit == "" {
				return 0, fmt.Errorf("operation state=%s commit=%q", record.State, record.GitCommit)
			}
			base = planned.Images[0].SemanticHash
			return len(record.GitCommit), nil
		}
	case "index_rebuild":
		repository, err := core.NewSYReadRepository(root, registry)
		if err != nil {
			return result, err
		}
		provider := core.NewClaimsAVProvider(repository, nil, registry)
		operation = func(_ int) (int, error) {
			snapshot, rebuildErr := provider.Rebuild(ctx)
			if rebuildErr != nil {
				return 0, rebuildErr
			}
			if snapshot.ClaimCount != claims || snapshot.EdgeCount != relations ||
				snapshot.ReverseCount != relations {
				return 0, fmt.Errorf("rebuild counts=%+v", snapshot)
			}
			return snapshot.ClaimCount, nil
		}
	}

	runtime.GC()
	for iteration := -warmup; iteration < 0; iteration++ {
		if _, err := operation(iteration); err != nil {
			return result, fmt.Errorf("%s warmup %d: %w", metric, iteration+warmup, err)
		}
	}
	durations := make([]time.Duration, 0, samples)
	for iteration := 0; iteration < samples; iteration++ {
		started := time.Now()
		observed, err := operation(iteration)
		duration := time.Since(started)
		if err != nil {
			return result, fmt.Errorf("%s sample %d: %w", metric, iteration, err)
		}
		result.ObservedCount = observed
		durations = append(durations, duration)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	result.P50MS = percentileMS(durations, 0.50)
	result.P95MS = percentileMS(durations, 0.95)
	result.P99MS = percentileMS(durations, 0.99)
	result.MaxMS = durationMS(durations[len(durations)-1])
	result.MaxRSSBytes = maxRSSBytes()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result.HeapAlloc = memory.HeapAlloc
	result.HeapSys = memory.HeapSys
	if metric == "plan_apply" {
		var err error
		result.CommitAfter, err = gitCommitCount(root)
		if err != nil {
			return result, err
		}
		if result.CommitAfter-result.CommitBefore != warmup+samples {
			return result, fmt.Errorf(
				"commit delta=%d, expected %d", result.CommitAfter-result.CommitBefore, warmup+samples)
		}
		status, err := gitOutput(root, "status", "--porcelain")
		if err != nil {
			return result, err
		}
		if strings.TrimSpace(status) != "" {
			return result, fmt.Errorf("plan/apply left dirty Git state: %s", status)
		}
	}
	return result, nil
}

func percentileMS(sorted []time.Duration, percentile float64) float64 {
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return durationMS(sorted[index])
}

func durationMS(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func editNodeData(raw []byte, targetID, data string) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	changed, err := editNodeObject(root, targetID, data)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, fmt.Errorf("node %q not found", targetID)
	}
	return json.Marshal(root)
}

func editNodeObject(node map[string]json.RawMessage, targetID, data string) (bool, error) {
	var id string
	_ = json.Unmarshal(node["ID"], &id)
	if id == targetID {
		node["Data"], _ = json.Marshal(data)
		return true, nil
	}
	var children []json.RawMessage
	if len(node["Children"]) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(node["Children"], &children); err != nil {
		return false, err
	}
	for index, child := range children {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(child, &object); err != nil {
			return false, err
		}
		changed, err := editNodeObject(object, targetID, data)
		if err != nil {
			return false, err
		}
		if changed {
			children[index], err = json.Marshal(object)
			if err != nil {
				return false, err
			}
			node["Children"], err = json.Marshal(children)
			return true, err
		}
	}
	return false, nil
}
