package timesheet

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// FormatTable renders the report as plain text for the console.
func FormatTable(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Timesheet %s to %s\n\n", r.From, r.To)
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", w)
	}
	if len(r.Warnings) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%-10s  %-10s  %6s  %5s  %-40s  %s\n", "date", "code", "raw", "hours", "name", "notes")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "%-10s  %-10s  %6.2f  %5.1f  %-40.40s  %s\n", row.Date, row.Code, row.RawHours, row.Hours, row.Name, strings.Join(row.Notes, "; "))
	}
	b.WriteString("\nPer day:\n")
	for _, d := range r.Days {
		fmt.Fprintf(&b, "  %s  projects %5.1f h  clock %.2f h\n", d.Date, d.ProjectHours, d.ClockHours)
	}
	if len(r.Unassigned) > 0 {
		b.WriteString("\nUnassigned (assign manually):\n")
		for _, row := range r.Unassigned {
			fmt.Fprintf(&b, "  %s  %6.2f h  %s\n", row.Date, row.RawHours, strings.Join(row.Notes, "; "))
		}
	}
	if len(r.Other) > 0 {
		b.WriteString("\nOther apps (not billed):\n")
		for _, row := range r.Other {
			fmt.Fprintf(&b, "  %s  %-30s  %6.2f h\n", row.Date, row.Code, row.RawHours)
		}
	}
	return b.String()
}

// WriteCSV writes billed rows, then unassigned rows, with a section column.
func WriteCSV(w io.Writer, r Report) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"section", "date", "code", "name", "raw_hours", "hours", "notes"})
	write := func(section string, rows []Row) {
		for _, row := range rows {
			cw.Write([]string{section, row.Date, row.Code, row.Name,
				fmt.Sprintf("%.2f", row.RawHours), fmt.Sprintf("%.1f", row.Hours), strings.Join(row.Notes, "; ")})
		}
	}
	write("billed", r.Rows)
	write("unassigned", r.Unassigned)
	cw.Flush()
	return cw.Error()
}
