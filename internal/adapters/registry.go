package adapters

import (
	"context"
	"sort"

	"github.com/yusefmosiah/cogent/internal/adapterapi"
	"github.com/yusefmosiah/cogent/internal/adapters/claude"
	"github.com/yusefmosiah/cogent/internal/adapters/native"
	"github.com/yusefmosiah/cogent/internal/core"
)

type Capabilities = adapterapi.Capabilities
type Diagnosis = adapterapi.Diagnosis

func CatalogFromConfig(cfg core.Config) []Diagnosis {
	entries := []Diagnosis{
		describeAdapter(context.Background(), claude.New(cfg.Adapters.Claude.Binary, cfg.Adapters.Claude.Enabled)),
		describeAdapter(context.Background(), native.New(cfg.Adapters.Native.Binary, cfg.Adapters.Native.Enabled)),
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Adapter < entries[j].Adapter
	})

	return entries
}

func Resolve(ctx context.Context, cfg core.Config, name string) (adapterapi.Adapter, Diagnosis, bool) {
	var adapter adapterapi.Adapter

	switch name {
	case "claude":
		adapter = claude.New(cfg.Adapters.Claude.Binary, cfg.Adapters.Claude.Enabled)
	case "native":
		adapter = native.New(cfg.Adapters.Native.Binary, cfg.Adapters.Native.Enabled)
	default:
		return nil, Diagnosis{}, false
	}

	diag, _ := adapter.Detect(ctx)

	return adapter, diag, true
}

func describeAdapter(ctx context.Context, adapter adapterapi.Adapter) Diagnosis {
	diag, _ := adapter.Detect(ctx)
	return diag
}
