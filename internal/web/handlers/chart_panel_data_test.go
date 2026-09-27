package handlers

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/chart"
)

var dataIsland = regexp.MustCompile(`(?s)<script type="application/json" class="c-chart-panel__data">(.*?)</script>`)

// islandOf parses the panel's JSON island out of a rendered fragment.
func islandOf(t *testing.T, out string) chart.PanelData {
	t.Helper()
	m := dataIsland.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no data island in\n%s", out)
	}
	var d chart.PanelData
	if err := json.Unmarshal([]byte(m[1]), &d); err != nil {
		t.Fatalf("island does not parse: %v\n%s", err, m[1])
	}
	return d
}

// Decision 9: the fragment carries what the hover needs as a typed
// JSON island, and it reads back as exactly what the server laid out.
func TestChartPanelTemplate_ShipsTheDataIsland(t *testing.T) {
	p := livePanel()
	if len(p.Data.Trace) != 4 {
		t.Fatalf("panel data trace = %d points, want the report's 4", len(p.Data.Trace))
	}
	if got := islandOf(t, renderPanel(t, p)); !reflect.DeepEqual(got, p.Data) {
		t.Errorf("island round trip = %+v, want %+v", got, p.Data)
	}
}

// A task title is user text; one containing </script> must not close
// the island early, and must still read back intact.
func TestChartPanelTemplate_DataIslandSurvivesAScriptCloseInATitle(t *testing.T) {
	rep := panelReport()
	rep.Imports[0].Title = `</script><img src=x onerror=alert(1)>`
	p := buildChartPanel("home", rep, nil, navAt(job.Range7D), chart.EndsNow, time.UTC)
	out := renderPanel(t, p)
	if strings.Contains(out, "<img src=x") {
		t.Fatalf("the title escaped the island:\n%s", out)
	}
	if n := strings.Count(out, "</script>"); n != 1 {
		t.Errorf("</script> appears %d times, want only the island's own close", n)
	}
	d := islandOf(t, out)
	if want := "Imported </script><img src=x onerror=alert(1)> from reporting.md"; d.Imports[0].Label != want {
		t.Errorf("import label = %q, want %q", d.Imports[0].Label, want)
	}
}

// Only a drawn panel carries the island; empty and error have nothing
// to hover.
func TestChartPanelTemplate_NoDataIslandWithoutACharts(t *testing.T) {
	for _, p := range []ChartPanel{
		buildChartPanel("home", job.Report{}, errors.New("disk on fire"), navAt(job.Range7D), chart.EndsNow, time.UTC),
		buildChartPanel("home", job.Report{Window: panelReport().Window}, nil, navAt(job.Range7D), chart.EndsNow, time.UTC),
	} {
		if out := renderPanel(t, p); strings.Contains(out, "c-chart-panel__data") {
			t.Errorf("state %q ships a data island:\n%s", p.State, out)
		}
	}
}

// Decision 6: leaves canceled in the window are a band on the scope
// line, drawn only when there are some.
func TestChartPanelTemplate_DrawsTheCanceledBand(t *testing.T) {
	mustHave(t, renderPanel(t, livePanel()), `<path class="c-burnup__canceled" d="M0 `)
	quiet := panelReport()
	for i := range quiet.Trace {
		quiet.Trace[i].Canceled = 0
	}
	p := buildChartPanel("home", quiet, nil, navAt(job.Range7D), chart.EndsNow, time.UTC)
	if out := renderPanel(t, p); strings.Contains(out, "c-burnup__canceled") {
		t.Errorf("a window with nothing canceled draws the band:\n%s", out)
	}
}

// The legend's counts take thousands separators, as the end labels do.
func TestChartPanelTemplate_LegendCountsUseThousandsSeparators(t *testing.T) {
	rep := panelReport()
	rep.Activity[0].Created = 1153
	out := renderPanel(t, buildChartPanel("home", rep, nil, navAt(job.Range7D), chart.EndsNow, time.UTC))
	// The count sits in its handle span (chart-panel-hover.mjs rewrites it).
	mustHave(t, out, `>1,161</span> created</li>`)
}
