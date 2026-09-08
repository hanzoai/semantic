package export

import (
	"context"
	"fmt"
	"io"
)

// Absent stands for a format this module knows of and cannot write, so asking
// for it says what is missing rather than that the name is unknown. parquet
// and yaml are registered this way: a columnar writer and a YAML encoder are
// both outside the standard library, and this module takes no dependencies.
//
// Registering a real writer under the name fills the gap:
//
//	export.Default.Set("parquet", myParquetWriter)
//
// Until then csv and ndjson carry every field either of them would.
type Absent struct{ Name string }

// Write reports that the format is not built in.
func (a Absent) Write(context.Context, io.Writer, Source) error {
	return fmt.Errorf("%s: %w", a.Name, ErrLibrary)
}
