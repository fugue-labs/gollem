// Command sync refreshes providers.json from the hosted catwalk registry.
//
// Usage (from the repository root):
//
//	go generate ./appserver/catalog/catwalk
//	go run ./appserver/catalog/catwalk/internal/cmd/sync -from providers.json.download
//
// Only provider types Gollem can drive are kept; see catwalk.SupportedType.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
)

func main() {
	var (
		from    = flag.String("from", "", "read the registry from this JSON file instead of fetching")
		out     = flag.String("out", "providers.json", "output path (relative to the package directory)")
		metaOut = flag.String("meta-out", "snapshot.json", "snapshot metadata output path")
		url     = flag.String("url", "", "registry base URL (default $CATWALK_URL or the hosted registry)")
	)
	flag.Parse()

	providers, err := load(*from, *url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "catwalk sync:", err)
		os.Exit(1)
	}
	kept := catwalk.FilterSupported(providers)
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "catwalk sync: encode:", err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil { // #nosec G306 -- generated source data
		fmt.Fprintln(os.Stderr, "catwalk sync: write:", err)
		os.Exit(1)
	}
	source := *from
	if source == "" {
		source = catwalk.NewClient(*url, nil).BaseURL() + "/v2/providers"
	}
	meta, err := json.MarshalIndent(catwalk.SnapshotInfo{GeneratedAt: time.Now().UTC().Truncate(time.Second), Source: source}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "catwalk sync: encode snapshot metadata:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*metaOut, append(meta, '\n'), 0o644); err != nil { // #nosec G306 -- generated source data
		fmt.Fprintln(os.Stderr, "catwalk sync: write snapshot metadata:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "catwalk sync: wrote %d of %d providers to %s\n", len(kept), len(providers), *out)
}

func load(from, url string) ([]catwalk.Provider, error) {
	if from != "" {
		data, err := os.ReadFile(from) // #nosec G304 -- operator-supplied path
		if err != nil {
			return nil, err
		}
		return catwalk.Parse(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	providers, _, err := catwalk.NewClient(url, nil).Fetch(ctx, "")
	return providers, err
}
