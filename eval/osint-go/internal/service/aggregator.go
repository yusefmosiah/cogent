package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/repository"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/scanner"
)

type ResultStore interface {
	Save(ctx context.Context, result model.ScanResult)
	Get(ctx context.Context, id string) (model.ScanResult, bool)
}

type Aggregator struct {
	scanners []scanner.Scanner
	store    ResultStore
	timeout  time.Duration
}

func NewAggregator(store ResultStore, timeout time.Duration, scanners ...scanner.Scanner) *Aggregator {
	return &Aggregator{
		scanners: scanners,
		store:    store,
		timeout:  timeout,
	}
}

func (a *Aggregator) Scan(ctx context.Context, domain string) (model.ScanResult, error) {
	if err := scanner.ValidateDomain(domain); err != nil {
		return model.ScanResult{}, err
	}
	if len(a.scanners) == 0 {
		return model.ScanResult{}, errors.New("no scanners configured")
	}

	scanCtx := ctx
	var cancel context.CancelFunc
	if a.timeout > 0 {
		scanCtx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}

	started := time.Now().UTC()
	result := model.ScanResult{
		ID:        newID(),
		Domain:    domain,
		StartedAt: started,
	}

	type item struct {
		finding model.Finding
		err     error
		source  string
	}

	ch := make(chan item, len(a.scanners))
	var wg sync.WaitGroup
	for _, sc := range a.scanners {
		sc := sc
		wg.Add(1)
		go func() {
			defer wg.Done()
			finding, err := sc.Scan(scanCtx, domain)
			ch <- item{finding: finding, err: err, source: sc.Name()}
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for entry := range ch {
		if entry.err != nil {
			result.Errors = append(result.Errors, model.SourceError{
				Source: entry.source,
				Error:  entry.err.Error(),
			})
			continue
		}
		result.Findings = append(result.Findings, entry.finding)
	}

	result.CompletedAt = time.Now().UTC()
	result.DurationMillis = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	result.PartialFailure = len(result.Errors) > 0

	if a.store != nil {
		a.store.Save(ctx, result)
	}
	return result, nil
}

func newID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	now := time.Now().UnixNano()
	r := rand.New(rand.NewSource(now))
	var b strings.Builder
	b.Grow(20)
	for i := 0; i < 10; i++ {
		b.WriteByte(alphabet[r.Intn(len(alphabet))])
	}
	b.WriteString(fmt.Sprintf("%x", now))
	return b.String()
}

var _ ResultStore = (*repository.Cache)(nil)
