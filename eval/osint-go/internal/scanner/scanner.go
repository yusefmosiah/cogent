package scanner

import (
	"context"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

type Scanner interface {
	Name() string
	Scan(ctx context.Context, domain string) (model.Finding, error)
}
