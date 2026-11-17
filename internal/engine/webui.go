package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

type apiSlot struct {
	Name      string  `json:"name"`
	IP        string  `json:"ip"`
	State     string  `json:"state"`
	RTTMs     float64 `json:"rtt_ms"`
	JitterMs  float64 `json:"jitter_ms"`
	RateIn    float64 `json:"rate_in_mbps"`
	RateOut   float64 `json:"rate_out_mbps"`
	LimitMbps float64 `json:"limit_mbps"`
	Active    int64   `json:"active"`
	Total     int64   `json:"total"`
	IsPrimary bool    `json:"is_primary"`
}

type apiResponse struct {
	SmartMode bool      `json:"smart_mode"`
	BestNIC   string    `json:"best_nic"`
	Slots     []apiSlot `json:"slots"`
}

// StartWebUI launches the local telemetry dashboard and mode switcher on 127.0.0.1:8899.
func StartWebUI(sched *Scheduler) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, dashboardHTML)
	})

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snaps := sched.Snapshot()
		best := sched.BestLowLatencyName(Fam4)
		resp := apiResponse{
			SmartMode: sched.IsSmartMode(),
			BestNIC:   best,
			Slots:     make([]apiSlot, 0, len(snaps)),
		}
		for _, s := range snaps {
			if !s.Present {
				continue
			}
			resp.Slots = append(resp.Slots, apiSlot{
				Name:      s.Name,
				IP:        s.IP,
				State:     s.State,
				RTTMs:     s.RTTMs,
				JitterMs:  s.JitterMs,
				RateIn:    (s.RateIn * 8) / 1e6,
				RateOut:   (s.RateOut * 8) / 1e6,
				LimitMbps: s.LimitMbps,
				Active:    s.Active,
				Total:     s.Total,
				IsPrimary: s.Name == best && s.Name != "",
			})
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/toggle", func(w http.ResponseWriter, r *http.Request) {
		next := !sched.IsSmartMode()
		sched.SetSmartMode(next)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"smart_mode": next})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:8899")
	if err != nil {
		return
	}

	go func() {
		time.Sleep(800 * time.Millisecond)
		openBrowser("http://127.0.0.1:8899")
	}()

	go func() {
		_ = http.Serve(ln, mux)
	}()
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Net-Merge Pro | Low-Latency Multi-WAN Telemetry</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; font-family: 'Segoe UI', -apple-system, BlinkMacSystemFont, sans-serif; }
  body { background: #0b0f19; color: #e2e8f0; padding: 24px; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; padding-bottom: 16px; border-bottom: 1px solid #1e293b; }
  .title h1 { font-size: 22px; color: #f8fafc; display: flex; align-items: center; gap: 10px; }
  .title p { font-size: 13px; color: #94a3b8; margin-top: 4px; }
  .mode-btn { padding: 10px 20px; border-radius: 8px; border: none; font-weight: 700; font-size: 14px; cursor: pointer; transition: all 0.2s; }
  .mode-on { background: #10b981; color: #022c22; box-shadow: 0 0 15px rgba(16,185,129,0.4); }
  .mode-off { background: #ef4444; color: #fff; box-shadow: 0 0 15px rgba(239,68,68,0.4); }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 18px; margin-bottom: 24px; }
  .card { background: #131b2e; border: 1px solid #1e293b; border-radius: 12px; padding: 18px; position: relative; }
  .card.primary { border-color: #10b981; box-shadow: 0 0 20px rgba(16,185,129,0.1); }
  .badge { position: absolute; top: 16px; right: 16px; padding: 4px 10px; border-radius: 20px; font-size: 12px; font-weight: 600; }
  .badge-primary { background: rgba(16,185,129,0.15); color: #34d399; border: 1px solid #059669; }
  .badge-bulk { background: rgba(59,130,246,0.15); color: #60a5fa; border: 1px solid #2563eb; }
  .nic-name { font-size: 17px; font-weight: 700; color: #f1f5f9; }
  .nic-ip { font-size: 12px; color: #64748b; margin-top: 2px; margin-bottom: 14px; }
  .metrics { display: grid; grid-template-columns: repeat(5, 1fr); gap: 8px; background: #0f1524; padding: 12px; border-radius: 8px; }
  .m-item { text-align: center; }
  .m-label { font-size: 11px; color: #94a3b8; margin-bottom: 4px; }
  .m-val { font-size: 15px; font-weight: 700; color: #f8fafc; }
  .rtt-good { color: #34d399; }
  .rtt-warn { color: #fbbf24; }
  .rtt-bad { color: #f87171; }
  .cap-val { color: #38bdf8; }
  .chart-box { background: #131b2e; border: 1px solid #1e293b; border-radius: 12px; padding: 18px; }
  .chart-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
  .chart-title { font-size: 14px; font-weight: 600; color: #cbd5e1; }
  .legend { display: flex; gap: 16px; font-size: 12px; }
  .legend span { display: flex; align-items: center; gap: 6px; }
  .dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
  canvas { width: 100%; height: 220px; display: block; background: #0b0f19; border-radius: 8px; }
</style>
</head>
<body>
  <div class="header">
    <div class="title">
      <h1>&#9889; Net-Merge Pro Low-Latency Multi-WAN Hub</h1>
      <p>Relay-free OS-level aggregation &bull; 1s physical RTT probing &bull; Adaptive AIMD Anti-Bufferbloat engine</p>
    </div>
    <button id="toggleBtn" class="mode-btn mode-on" onclick="toggleMode()">Mode: Low-Latency Gaming (Click to Switch)</button>
  </div>

  <div id="cards" class="grid"></div>

  <div class="chart-box">
    <div class="chart-header">
      <div class="chart-title">&#128202; Real-Time Physical Adapter RTT Monitor (ms)</div>
      <div id="legend" class="legend"></div>
    </div>
    <canvas id="rttChart" width="900" height="220"></canvas>
  </div>

<script>
const historyMap = {};
const colors = ['#10b981', '#3b82f6', '#f59e0b', '#ec4899'];
const maxPoints = 60;

async function toggleMode() {
  await fetch('/api/toggle');
  fetchStats();
}

function rttClass(ms) {
  if (ms <= 0) return '';
  if (ms < 45) return 'rtt-good';
  if (ms < 100) return 'rtt-warn';
  return 'rtt-bad';
}

async function fetchStats() {
  try {
    const res = await fetch('/api/stats');
    const data = await res.json();

    const btn = document.getElementById('toggleBtn');
    if (data.smart_mode) {
      btn.className = 'mode-btn mode-on';
      btn.textContent = 'Mode: Low-Latency Gaming (Click to Switch)';
    } else {
      btn.className = 'mode-btn mode-off';
      btn.textContent = 'Mode: Original Blind Aggregation (Click to Switch)';
    }

    const cardsEl = document.getElementById('cards');
    const legendEl = document.getElementById('legend');
    cardsEl.innerHTML = '';
    legendEl.innerHTML = '';

    data.slots.forEach((s, idx) => {
      const color = colors[idx % colors.length];
      if (!historyMap[s.name]) historyMap[s.name] = [];
      historyMap[s.name].push(s.rtt_ms > 0 ? s.rtt_ms : null);
      if (historyMap[s.name].length > maxPoints) historyMap[s.name].shift();

      const badge = s.is_primary
        ? '<span class="badge badge-primary">&#127918; Low-Latency Primary Lane</span>'
        : '<span class="badge badge-bulk">&#128230; Bulk Download Offload Lane</span>';

      const capText = s.limit_mbps > 0 ? s.limit_mbps.toFixed(1) + ' Mbps' : 'Unlimited';

      cardsEl.innerHTML += ` + "`" + `
        <div class="card ${s.is_primary ? 'primary' : ''}">
          ${badge}
          <div class="nic-name">${s.name}</div>
          <div class="nic-ip">IP: ${s.ip || 'N/A'} | Status: ${s.state}</div>
          <div class="metrics">
            <div class="m-item"><div class="m-label">Physical RTT</div><div class="m-val ${rttClass(s.rtt_ms)}">${s.rtt_ms > 0 ? s.rtt_ms.toFixed(1) + ' ms' : '-'}</div></div>
            <div class="m-item"><div class="m-label">Jitter</div><div class="m-val">${s.jitter_ms > 0 ? s.jitter_ms.toFixed(1) + ' ms' : '-'}</div></div>
            <div class="m-item"><div class="m-label">Download</div><div class="m-val">${s.rate_in_mbps.toFixed(1)} Mbps</div></div>
            <div class="m-item"><div class="m-label">Rate Cap</div><div class="m-val cap-val">${capText}</div></div>
            <div class="m-item"><div class="m-label">Active Flows</div><div class="m-val">${s.active}</div></div>
          </div>
        </div>
      ` + "`" + `;

      legendEl.innerHTML += ` + "`" + `<span><i class="dot" style="background:${color}"></i>${s.name}</span>` + "`" + `;
    });

    drawChart(data.slots);
  } catch (e) {}
}

function drawChart(slots) {
  const canvas = document.getElementById('rttChart');
  const ctx = canvas.getContext('2d');
  const w = canvas.width, h = canvas.height;
  ctx.clearRect(0, 0, w, h);

  let maxVal = 100;
  Object.values(historyMap).forEach(arr => {
    arr.forEach(v => { if (v && v > maxVal) maxVal = Math.ceil(v / 50) * 50; });
  });

  ctx.strokeStyle = '#1e293b';
  ctx.lineWidth = 1;
  ctx.fillStyle = '#64748b';
  ctx.font = '11px sans-serif';
  for (let i = 1; i <= 4; i++) {
    const y = h - (h * i / 4);
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(w, y);
    ctx.stroke();
    ctx.fillText(Math.round(maxVal * i / 4) + ' ms', 8, y - 4);
  }

  slots.forEach((s, idx) => {
    const arr = historyMap[s.name] || [];
    if (arr.length < 2) return;
    ctx.strokeStyle = colors[idx % colors.length];
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    let started = false;
    arr.forEach((val, i) => {
      if (val === null) return;
      const x = (i / (maxPoints - 1)) * w;
      const y = h - (val / maxVal) * (h - 20) - 10;
      if (!started) { ctx.moveTo(x, y); started = true; }
      else { ctx.lineTo(x, y); }
    });
    ctx.stroke();
  });
}

setInterval(fetchStats, 1000);
fetchStats();
</script>
</body>
</html>
`
