// Command pipelinetry runs a content pipeline through every stage and prints
// what each one produced. Each stage prints before the next runs, so a failure
// is attributable to a stage. Not part of the service.
//
// Discover the available fields:
//
//	go run ./cmd/pipelinetry -curl "curl 'https://dummyjson.com/products/1'"
//
// Map the ones you want, and persist the result:
//
//	go run ./cmd/pipelinetry -curl "curl 'https://dummyjson.com/products/1'" \
//	  -map 'title:title:string,price:price:number' -save
//
// Run it again later from Postgres, with no cURL:
//
//	go run ./cmd/pipelinetry -load <pipelineID> -version 1
//
// Make a path segment dynamic, then vary it on the stored config:
//
//	go run ./cmd/pipelinetry -curl "curl 'https://dummyjson.com/products/1'" \
//	  -dynamic 1=productId -param productId=5 -map 'title:title:string' -save
//	go run ./cmd/pipelinetry -load <pipelineID> -version 1 -param productId=9
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"content-pipeline-insider/internal/config"
	"content-pipeline-insider/internal/curlimport"
	"content-pipeline-insider/internal/draft"
	"content-pipeline-insider/internal/fetcher"
	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/pipeline"
	"content-pipeline-insider/internal/resolver"
	"content-pipeline-insider/internal/schemainfer"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/store"
	"content-pipeline-insider/internal/upstream"
)

type options struct {
	curlCmd string
	loadID  string
	version int

	mapSpec  string
	tenantNS string
	draftNS  string

	save      bool
	name      string
	tenantID  string
	createdBy string

	params    map[string]string
	secretsIn map[string]string
	dynamics  dynamicFlag
}

func main() {
	var (
		o         options
		timeout   = flag.Duration("timeout", 20*time.Second, "overall deadline for the run")
		params    = kvFlag{}
		secretsIn = kvFlag{}
		dynamics  = &dynamicFlag{}
	)
	flag.StringVar(&o.curlCmd, "curl", "", "the cURL command to import")
	flag.StringVar(&o.loadID, "load", "", "pipeline UUID to load from Postgres instead of importing a cURL")
	flag.IntVar(&o.version, "version", 1, "version number to load")
	flag.StringVar(&o.mapSpec, "map", "", "comma-separated outputName:jmesPath:dataType")
	flag.StringVar(&o.tenantNS, "tenant", "tenant-local", "namespace for secret references")
	flag.StringVar(&o.draftNS, "draft", "draft-local", "namespace for secret references")
	flag.BoolVar(&o.save, "save", false, "persist the pipeline and its draft version to Postgres")
	flag.StringVar(&o.name, "name", "", "pipeline name when saving (defaults to the URL host)")
	flag.StringVar(&o.tenantID, "tenant-id", "", "tenant UUID when saving (generated if omitted)")
	flag.StringVar(&o.createdBy, "created-by", "", "author UUID when saving (generated if omitted)")
	flag.Var(params, "param", "value for a dynamic parameter, name=value (repeatable)")
	flag.Var(secretsIn, "secret", "supply a stored secret, reference=value (repeatable)")
	flag.Var(dynamics, "dynamic", "make a path segment dynamic, index=paramName (repeatable)")
	flag.Parse()

	o.params, o.secretsIn, o.dynamics = params, secretsIn, *dynamics

	switch {
	case o.curlCmd == "" && o.loadID == "":
		usage("one of -curl or -load is required")
	case o.curlCmd != "" && o.loadID != "":
		usage("-curl and -load are mutually exclusive")
	case o.save && o.loadID != "":
		usage("-save applies to an imported cURL, not to a config already stored")
	case o.save && o.mapSpec == "":
		usage("-save requires -map: a version with no fields cannot render anything")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "\n"+err.Error())
		os.Exit(1)
	}
}

func usage(problem string) {
	fmt.Fprintln(os.Stderr, "error: "+problem+"\n")
	fmt.Fprintln(os.Stderr, `usage: pipelinetry -curl "curl ..." [-map out:path:type,...] [-save]`)
	fmt.Fprintln(os.Stderr, `       pipelinetry -load <pipelineID> [-version N] [-param k=v]`)
	flag.PrintDefaults()
	os.Exit(2)
}

func run(ctx context.Context, o options) error {
	// Pre-seeded with any -secret values, which is how a loaded config gets
	// credentials back: the stored config holds only references.
	resolv := secrets.NewMemoryResolver(o.secretsIn)

	var (
		cfg    upstream.UpstreamConfig
		set    []mappings.FieldMapping
		source string
		err    error
	)

	if o.loadID != "" {
		cfg, set, source, err = loadFromDB(ctx, o)
	} else {
		cfg, source, err = importFromCurl(ctx, o, resolv)
	}
	if err != nil {
		return err
	}

	// A loaded version brings its own selection; an imported one takes it from
	// -map. A nil set means "stop after discovery".
	if set == nil && o.mapSpec != "" {
		if set, err = parseMappings(o.mapSpec); err != nil {
			return fmt.Errorf("mapping spec invalid: %w", err)
		}
	}

	stage("3-5. RESOLVE — build, fetch, parse, discover")

	res, err := resolver.New(fetcher.New(fetcher.Options{}), resolv).
		Resolve(ctx, cfg, set, o.params)
	if err != nil {
		if refs := secretReferences(cfg); len(refs) > 0 && res == nil {
			return fmt.Errorf("resolve failed: %w\n\nthis config needs secrets that are not in this "+
				"process. Re-supply them with:\n  -secret '%s=<value>'", err, refs[0])
		}
		if res != nil && res.Tree == nil && len(res.Body) > 0 {
			fmt.Printf("  status  : %d\n\n  body:\n%s\n", res.Status, indent(string(res.Body)))
		}
		return fmt.Errorf("resolve failed: %w", err)
	}

	fmt.Printf("  status  : %d\n", res.Status)
	fmt.Printf("  elapsed : %s\n", res.Elapsed.Round(time.Millisecond))
	fmt.Println("\n  body:")
	fmt.Println(indent(string(res.Body)))

	fmt.Println("\n  field tree (• = selectable)")
	fmt.Print(indent(res.Tree.String()))

	fmt.Println("\n  selectable fields — this is the admin's picking screen:")
	for _, fd := range res.Fields {
		fmt.Printf("    %-38s %-8s  e.g. %v\n", fd.JMESPath, fd.Type, truncate(fd.SampleValue))
	}

	if res.Output == nil {
		fmt.Println("\nNo -map given, so the run stops at discovery.")
		fmt.Println("Pick fields from the list above and re-run, for example:")
		fmt.Printf("  -map '%s'\n", exampleMapSpec(res.Fields))
		return nil
	}

	stage("6-7. OUTPUT — the normalized object a template renders")

	printJSON(set)
	printJSON(res.Output)

	fmt.Println("\n  as a template would reference them:")
	names := make([]string, 0, len(res.Output))
	for k := range res.Output {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Printf("    {{ product.%s }} -> %v\n", k, res.Output[k])
	}

	if o.save {
		source, err = saveToDB(ctx, o, cfg, set)
		if err != nil {
			return err
		}
	}

	recordEvent(ctx, source, res.Status, res.Elapsed)
	return nil
}

func importFromCurl(ctx context.Context, o options, secretStore secrets.Storer) (upstream.UpstreamConfig, string, error) {
	stage("1. IMPORT — parse the cURL command")

	imported, err := curlimport.Parse(o.curlCmd)
	if err != nil {
		return upstream.UpstreamConfig{}, "", fmt.Errorf("import failed: %w", err)
	}
	// Redacted, always. The plaintext form exists only to feed the store below.
	printJSON(imported.Redacted())

	if segments := imported.PathSegments(); len(segments) > 0 {
		fmt.Println("\n  path segments (candidates for -dynamic):")
		for i, seg := range segments {
			fmt.Printf("    [%d] %s\n", i, seg)
		}
	}

	stage("2. DRAFT — build the upstream config, lift secrets out")

	cfg, err := draft.FromImported(ctx, *imported, o.tenantNS, o.draftNS, secretStore)
	if err != nil {
		return upstream.UpstreamConfig{}, "", fmt.Errorf("draft conversion failed: %w", err)
	}

	for _, d := range o.dynamics {
		cfg, err = draft.MakePathDynamic(cfg, d.index, d.name)
		if err != nil {
			return upstream.UpstreamConfig{}, "", fmt.Errorf("make dynamic failed: %w", err)
		}
		fmt.Printf("  path[%d] is now {%s}\n", d.index, d.name)
	}

	printJSON(cfg)
	if refs := secretReferences(cfg); len(refs) > 0 {
		fmt.Println("\n  secret references created (values live in the store, not above):")
		for _, r := range refs {
			fmt.Println("    " + r)
		}
	}
	return cfg, imported.BaseURL, nil
}

func loadFromDB(ctx context.Context, o options) (upstream.UpstreamConfig, []mappings.FieldMapping, string, error) {
	stage("1-2. LOAD — read the stored version from Postgres")

	pipelineID, err := uuid.Parse(o.loadID)
	if err != nil {
		return upstream.UpstreamConfig{}, nil, "", fmt.Errorf("-load %q is not a UUID: %w", o.loadID, err)
	}

	st, err := openStore(ctx)
	if err != nil {
		return upstream.UpstreamConfig{}, nil, "", err
	}
	defer st.Close()

	v, err := pipeline.NewRepository(st.Pool()).GetVersion(ctx, pipelineID, o.version)
	if err != nil {
		return upstream.UpstreamConfig{}, nil, "", fmt.Errorf("load failed: %w", err)
	}

	fmt.Printf("  pipeline : %s\n", v.PipelineID)
	fmt.Printf("  version  : %d (%s)\n", v.VersionNumber, v.Status)
	fmt.Printf("  created  : %s by %s\n", v.CreatedAt.Format(time.RFC3339), v.CreatedBy)
	fmt.Printf("  fields   : %d\n", len(v.Mappings))
	printJSON(v.Upstream)

	if refs := secretReferences(v.Upstream); len(refs) > 0 {
		fmt.Println("\n  this config references secrets — supply them with -secret ref=value:")
		for _, r := range refs {
			fmt.Println("    " + r)
		}
	}
	return v.Upstream, v.Mappings, v.PipelineID.String(), nil
}

func saveToDB(ctx context.Context, o options, cfg upstream.UpstreamConfig, set []mappings.FieldMapping) (string, error) {
	stage("8. SAVE — persist the pipeline and its draft version")

	tenantID, err := uuidOrNew(o.tenantID, "-tenant-id")
	if err != nil {
		return "", err
	}
	createdBy, err := uuidOrNew(o.createdBy, "-created-by")
	if err != nil {
		return "", err
	}

	name := o.name
	if name == "" {
		// Unique by construction: the pipelines table rejects a duplicate
		// name within a tenant, and this command creates a new one each run.
		name = fmt.Sprintf("%s-%d", hostOf(cfg.URLTemplate), time.Now().Unix())
	}

	st, err := openStore(ctx)
	if err != nil {
		return "", err
	}
	defer st.Close()

	repo := pipeline.NewRepository(st.Pool())

	p, err := repo.CreatePipeline(ctx, tenantID, name)
	if err != nil {
		return "", fmt.Errorf("create pipeline failed: %w", err)
	}
	v, err := repo.SaveDraftVersion(ctx, p.ID, createdBy, cfg, set)
	if err != nil {
		return "", fmt.Errorf("save draft failed: %w", err)
	}

	fmt.Printf("  pipeline : %s\n", p.ID)
	fmt.Printf("  name     : %s\n", p.Name)
	fmt.Printf("  tenant   : %s\n", tenantID)
	fmt.Printf("  version  : %d (%s)\n", v.VersionNumber, v.Status)
	fmt.Printf("\n  load it back with:\n    go run ./cmd/pipelinetry -load %s -version %d\n",
		p.ID, v.VersionNumber)

	return p.ID.String(), nil
}

// recordEvent writes the audit row. A failed insert is reported and ignored:
// losing an audit row is not worth discarding a run that already succeeded.
func recordEvent(ctx context.Context, sourceID string, status int, elapsed time.Duration) {
	st, err := openStore(ctx)
	if err != nil {
		fmt.Printf("\n(no render_events row: %v)\n", err)
		return
	}
	defer st.Close()

	err = st.InsertRenderEvent(ctx, store.RenderEvent{
		SourceID:   sourceID,
		Cached:     false, // there is no cache yet
		DurationMS: int(elapsed.Milliseconds()),
		Status:     status,
		RenderedAt: time.Now(),
	})
	if err != nil {
		fmt.Printf("\n(render_events insert failed: %v)\n", err)
		return
	}
	fmt.Printf("\nrender_events row written for source %q\n", sourceID)
}

func openStore(ctx context.Context) (*store.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("database unavailable (try `make db-up && make migrate`): %w", err)
	}
	return st, nil
}

func uuidOrNew(raw, flagName string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.New(), nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s %q is not a UUID: %w", flagName, raw, err)
	}
	return id, nil
}

func hostOf(urlTemplate string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(urlTemplate, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "pipeline"
	}
	return s
}

// parseMappings reads outputName:jmesPath:dataType. A JMESPath containing a
// colon cannot be expressed here; that is a limit of this tool, not of the
// mapping model.
func parseMappings(spec string) ([]mappings.FieldMapping, error) {
	var set []mappings.FieldMapping
	for i, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%q: expected outputName:jmesPath:dataType", entry)
		}
		name := strings.TrimSpace(parts[0])
		set = append(set, mappings.FieldMapping{
			OutputName:   name,
			SourcePath:   strings.TrimSpace(parts[1]),
			DataType:     mappings.DataType(strings.TrimSpace(parts[2])),
			DisplayLabel: name,
			SortOrder:    i,
		})
	}
	return set, nil
}

// exampleMapSpec builds a runnable -map value from the first few selectable
// fields, so the second pass is copy-paste rather than typing.
func exampleMapSpec(fields []schemainfer.SchemaNode) string {
	var parts []string
	for _, f := range fields {
		if len(parts) == 3 {
			break
		}
		dt, ok := dataTypeFor(f.Type)
		if !ok || f.Name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%s:%s", f.Name, f.JMESPath, dt))
	}
	return strings.Join(parts, ",")
}

// dataTypeFor narrows a discovered type to a selectable one: objects, arrays,
// nulls and mixed have no data type a mapping can declare.
func dataTypeFor(t schemainfer.Type) (mappings.DataType, bool) {
	switch t {
	case schemainfer.TypeString:
		return mappings.TypeString, true
	case schemainfer.TypeInteger:
		return mappings.TypeInteger, true
	case schemainfer.TypeNumber:
		return mappings.TypeNumber, true
	case schemainfer.TypeBoolean:
		return mappings.TypeBoolean, true
	}
	return "", false
}

func secretReferences(cfg upstream.UpstreamConfig) []string {
	var refs []string
	for _, h := range cfg.Headers {
		if h.SecretReference != "" {
			refs = append(refs, h.SecretReference)
		}
	}
	for _, q := range cfg.QueryParameters {
		if q.SecretReference != "" {
			refs = append(refs, q.SecretReference)
		}
	}
	if cfg.Authentication != nil {
		refs = append(refs, cfg.Authentication.SecretReference)
	}
	return refs
}

type kvFlag map[string]string

func (k kvFlag) String() string { return fmt.Sprint(map[string]string(k)) }

func (k kvFlag) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("expected name=value, got %q", s)
	}
	k[strings.TrimSpace(name)] = value
	return nil
}

type dynamicSpec struct {
	index int
	name  string
}

// dynamicFlag preserves order: each rewrite applies to the previous result.
type dynamicFlag []dynamicSpec

func (d *dynamicFlag) String() string { return fmt.Sprint(*d) }

func (d *dynamicFlag) Set(s string) error {
	rawIndex, name, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected index=paramName, got %q", s)
	}
	i, err := strconv.Atoi(strings.TrimSpace(rawIndex))
	if err != nil {
		return fmt.Errorf("index %q is not a number", rawIndex)
	}
	*d = append(*d, dynamicSpec{index: i, name: strings.TrimSpace(name)})
	return nil
}

func stage(title string) {
	pad := 68 - len(title)
	if pad < 0 {
		pad = 0
	}
	fmt.Printf("\n=== %s %s\n", title, strings.Repeat("=", pad))
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal error:", err)
		return
	}
	fmt.Println("  " + string(b))
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func truncate(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 50 {
		return s[:50] + "…"
	}
	return s
}
