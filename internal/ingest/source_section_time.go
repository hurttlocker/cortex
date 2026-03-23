package ingest

import "github.com/hurttlocker/cortex/internal/temporal"

func timestampStartFromSourceSection(section string) string {
	return temporal.TimestampStartFromSection(section)
}
