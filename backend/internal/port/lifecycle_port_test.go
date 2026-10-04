package port

import (
	"archive/zip"
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// *zip.Writer is the production ExportSink (httpapi wraps the response in
// one); this pins the Create(name) (io.Writer, error) shape.
func TestZipWriterIsExportSink(t *testing.T) {
	var _ ExportSink = (*zip.Writer)(nil)
}

type stubExports struct{}

func (stubExports) Claim(context.Context, string, time.Time, time.Duration) (bool, time.Time, error) {
	return true, time.Time{}, nil
}

type stubLifecycle struct{}

func (stubLifecycle) Purge(context.Context, string) (PurgeReport, error) { return PurgeReport{}, nil }

func TestLifecyclePortShapes(t *testing.T) {
	var _ UserExportRepo = stubExports{}
	var _ UserLifecycleService = stubLifecycle{}
	var _ = domain.ErrOwnsTeams
}
