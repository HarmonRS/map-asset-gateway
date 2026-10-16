package basemapapi

const adminTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>底图网关 · 运维台</title>
<style>
  :root {
    --ground: #070c12;
    --panel: #0e161f;
    --panel-2: #121d29;
    --line: #1d2a39;
    --line-2: #2b3d51;
    --ink: #e8eef6;
    --ink-2: #adbccb;
    --ink-3: #8496a9;
    --accent: #35c6c0;
    --accent-ink: #04211f;
    --ok: #3ddc84;
    --warn: #ffd23f;
    --bad: #ff3b57;
    --r-0: #24414a;
    --r-1: #2fb3a3;
    --r-2: #3ddc84;
    --r-3: #ffd23f;
    --r-4: #ff8a2b;
    --r-5: #ff3b57;
    --r-6: #d64bff;
    --z-rail: 20;
    --z-top: 30;
    --z-toast: 60;
    --font: "Segoe UI", "Microsoft YaHei UI", system-ui, -apple-system, sans-serif;
    --mono: "Cascadia Mono", "Consolas", ui-monospace, monospace;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; }
  body {
    margin: 0;
    background: var(--ground);
    color: var(--ink);
    font: 400 13px/1.55 var(--font);
    font-variant-numeric: tabular-nums;
    -webkit-font-smoothing: antialiased;
  }
  a { color: var(--accent); text-decoration-thickness: 1px; text-underline-offset: 2px; }
  a:hover { color: #6fe3de; }
  ::selection { background: var(--accent); color: var(--accent-ink); }
  :focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 3px; }
  input, textarea, select, button { font: inherit; color: inherit; }
  input, textarea, select { caret-color: var(--accent); }
  * { scrollbar-color: var(--line-2) transparent; scrollbar-width: thin; }
  ::-webkit-scrollbar { width: 10px; height: 10px; }
  ::-webkit-scrollbar-thumb { background: var(--line-2); border: 3px solid var(--ground); border-radius: 6px; }
  ::-webkit-scrollbar-thumb:hover { background: #3b5069; }
  ::-webkit-scrollbar-track { background: transparent; }

  .skip { position: absolute; left: -9999px; top: 0; background: var(--accent); color: var(--accent-ink); padding: 8px 14px; z-index: var(--z-toast); }
  .skip:focus { left: 8px; top: 8px; }

  .shell { display: grid; grid-template-columns: 232px minmax(0, 1fr); min-height: 100vh; }

  .rail {
    position: sticky; top: 0; height: 100vh; overflow-y: auto;
    background: var(--panel);
    border-right: 1px solid var(--line);
    display: flex; flex-direction: column; gap: 4px; padding: 14px 10px 18px;
  }
  .brand { padding: 4px 8px 14px; }
  .brand b { display: block; font-size: 15px; font-weight: 650; letter-spacing: -0.01em; }
  .brand span { display: block; margin-top: 3px; color: var(--ink-3); font-size: 11px; letter-spacing: 0.06em; text-transform: uppercase; }
  .nav-label { padding: 12px 8px 5px; color: var(--ink-3); font-size: 10.5px; letter-spacing: 0.1em; text-transform: uppercase; }
  .nav-item {
    display: flex; align-items: center; gap: 9px;
    padding: 7px 9px; border-radius: 5px;
    color: var(--ink-2); text-decoration: none; font-size: 13px;
    border: 1px solid transparent;
  }
  .nav-item:hover { background: var(--panel-2); color: var(--ink); }
  .nav-item[aria-current="page"] { background: #16242f; color: var(--ink); border-color: var(--line-2); }
  .nav-item[aria-current="page"] svg { color: var(--accent); }
  .nav-item svg { width: 15px; height: 15px; flex: none; color: var(--ink-3); }
  .nav-item .n { margin-left: auto; color: var(--ink-3); font-size: 11px; font-family: var(--mono); }
  .rail-foot { margin-top: auto; padding: 12px 8px 0; border-top: 1px solid var(--line); color: var(--ink-3); font-size: 11px; }
  .main { min-width: 0; }
  .topbar {
    position: sticky; top: 0; z-index: var(--z-top);
    display: flex; align-items: center; gap: 14px; flex-wrap: wrap;
    padding: 10px 22px;
    background: rgba(7, 12, 18, 0.92);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--line);
  }
  .topbar h1 { margin: 0; font-size: 14px; font-weight: 600; letter-spacing: -0.005em; }
  .topbar .spacer { flex: 1 1 auto; }
  .chip {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 3px 9px; border: 1px solid var(--line-2); border-radius: 999px;
    color: var(--ink-2); font-size: 11.5px; background: var(--panel);
  }
  .chip .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--idle, #64768a); }
  .chip.is-ok .dot { background: var(--ok); }
  .chip.is-bad .dot { background: var(--bad); }

  .views { padding: 20px 22px 64px; max-width: 1500px; }
  .view[hidden] { display: none; }
  .view.sweep { animation: sweep 260ms cubic-bezier(0.16, 1, 0.3, 1) both; }
  @keyframes sweep {
    from { opacity: 0; transform: translateY(-6px); clip-path: inset(0 0 84% 0); }
    to { opacity: 1; transform: none; clip-path: inset(0 0 0 0); }
  }
  @media (prefers-reduced-motion: reduce) { .view.sweep { animation: none; } }

  h2 { margin: 0; font-size: 15px; font-weight: 620; letter-spacing: -0.01em; }
  label { display: block; margin-bottom: 3px; color: var(--ink-2); font-size: 11.5px; font-weight: 550; }
  .lead { color: var(--ink-2); margin: 6px 0 0; max-width: 72ch; }
  .sec { margin: 0 0 26px; }
  .sec-head { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
  .sec-head .count { color: var(--ink-3); font-size: 11.5px; font-family: var(--mono); }
  .sec-head .spacer { flex: 1 1 auto; }

  .band { background: var(--panel); border: 1px solid var(--line); border-radius: 7px; }
  .band-body { padding: 14px; }
  .split { display: grid; grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr); gap: 18px; align-items: start; }
  @media (max-width: 1080px) { .split { grid-template-columns: minmax(0, 1fr); } }
  @media (max-width: 860px) {
    .shell { grid-template-columns: minmax(0, 1fr); }
    .rail { position: static; height: auto; flex-direction: row; flex-wrap: wrap; align-items: center; border-right: 0; border-bottom: 1px solid var(--line); }
    .brand, .nav-label, .rail-foot { display: none; }
    .views, .topbar { padding-left: 14px; padding-right: 14px; }
  }

  .scan { display: flex; align-items: flex-end; gap: 3px; height: 46px; }
  .scan { justify-content: flex-start; }
  .scan i { flex: 1 1 0; max-width: 46px; min-width: 4px; border-radius: 2px 2px 0 0; background: var(--r-1); opacity: 0.9; }
  .scan i.ramp-ok { background: var(--r-2); }
  .scan i.ramp-warn { background: var(--r-3); }
  .scan i.ramp-bad { background: var(--r-5); }
  .scan i.ramp-idle { background: var(--r-0); }
  .scan-scale { display: flex; justify-content: space-between; margin-top: 6px; color: var(--ink-3); font-size: 10.5px; font-family: var(--mono); }
  .legend { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 10px; }
  .legend span { display: inline-flex; align-items: center; gap: 6px; color: var(--ink-2); font-size: 11.5px; }
  .legend i { width: 10px; height: 10px; border-radius: 2px; }

  .metrics { display: grid; grid-template-columns: repeat(auto-fit, minmax(148px, 1fr)); gap: 1px; background: var(--line); border: 1px solid var(--line); border-radius: 7px; overflow: hidden; }
  .metric { background: var(--panel); padding: 11px 13px; }
  .metric b { display: block; font-size: 19px; font-weight: 620; font-family: var(--mono); letter-spacing: -0.02em; }
  .metric span { display: block; margin-top: 2px; color: var(--ink-2); font-size: 11.5px; }
  .metric.is-bad b { color: var(--bad); }
  .metric.is-warn b { color: var(--warn); }

  ul.attention { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
  ul.attention li { display: flex; align-items: baseline; gap: 10px; padding: 9px 14px; border-bottom: 1px solid var(--line); }
  ul.attention li:last-child { border-bottom: 0; }
  ul.attention .what { font-weight: 550; }
  ul.attention .why { color: var(--ink-2); font-size: 12px; }
  ul.attention .at { margin-left: auto; color: var(--ink-3); font-size: 11.5px; font-family: var(--mono); white-space: nowrap; }

  .badge { display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; border-radius: 999px; font-size: 11.5px; font-weight: 600; border: 1px solid transparent; white-space: nowrap; }
  .badge.success { background: rgba(61,220,132,0.16); color: var(--ok); border-color: rgba(61,220,132,0.4); }
  .badge.warning { background: rgba(255,210,63,0.16); color: var(--warn); border-color: rgba(255,210,63,0.4); }
  .badge.danger { background: rgba(255,59,87,0.16); color: #ff8a9c; border-color: rgba(255,59,87,0.45); }
  .badge.neutral { background: rgba(132,150,169,0.14); color: var(--ink-2); border-color: var(--line-2); }

  .notice { padding: 10px 13px; border-radius: 6px; margin-bottom: 16px; border: 1px solid var(--line-2); background: var(--panel-2); }
  .notice.danger { border-color: rgba(255,59,87,0.5); background: rgba(255,59,87,0.1); color: #ffb3bf; }
  .mint { border: 1px solid rgba(53,198,192,0.5); background: rgba(53,198,192,0.09); border-radius: 7px; padding: 13px 14px; margin-bottom: 18px; }
  .mint code { display: block; margin: 8px 0 10px; padding: 9px 11px; background: var(--ground); border: 1px solid var(--line-2); border-radius: 5px; font-family: var(--mono); font-size: 12.5px; word-break: break-all; color: var(--ink); }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; padding: 8px 13px; color: var(--ink-3); font-size: 11px; font-weight: 600; letter-spacing: 0.05em; text-transform: uppercase; border-bottom: 1px solid var(--line); white-space: nowrap; }
  td { padding: 10px 13px; border-bottom: 1px solid var(--line); vertical-align: top; }
  tbody tr:last-child td { border-bottom: 0; }
  tbody tr:hover { background: var(--panel-2); }
  .t-wrap { overflow-x: auto; }
  .mono { font-family: var(--mono); font-size: 12px; }
  .small { font-size: 11.5px; }
  .muted { color: var(--ink-3); }
  .break { word-break: break-all; }
  .nowrap { white-space: nowrap; }
  .row-actions { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .stack { display: flex; flex-direction: column; gap: 7px; }
  .grid-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 10px 12px; align-items: end; }
  input[type="text"], input[type="number"], input:not([type]), textarea, select {
    width: 100%; padding: 7px 9px; background: var(--ground);
    border: 1px solid var(--line-2); border-radius: 5px; color: var(--ink);
  }
  input::placeholder, textarea::placeholder { color: var(--ink-3); }
  input:disabled, textarea:disabled, select:disabled { opacity: 0.5; cursor: not-allowed; }
  textarea { min-height: 62px; resize: vertical; font-family: var(--mono); font-size: 12px; }
  .field-note { margin-top: 3px; color: var(--ink-3); font-size: 11px; }
  button {
    padding: 7px 13px; border-radius: 5px; cursor: pointer;
    background: var(--accent); color: var(--accent-ink); border: 1px solid var(--accent);
    font-weight: 620; font-size: 12.5px;
    transition: filter 120ms ease-out;
  }
  button:hover { filter: brightness(1.12); }
  button:active { transform: translateY(1px); }
  button[disabled] { opacity: 0.45; cursor: not-allowed; }
  .btn-ghost { background: transparent; color: var(--ink-2); border-color: var(--line-2); font-weight: 500; }
  .btn-ghost:hover { background: var(--panel-2); color: var(--ink); filter: none; }
  .btn-quiet { background: transparent; border-color: transparent; color: var(--ink-2); padding: 5px 8px; }
  .btn-quiet:hover { background: var(--panel-2); color: var(--ink); filter: none; }
  .btn-danger { background: transparent; color: #ff8a9c; border-color: rgba(255,59,87,0.45); font-weight: 500; }
  .btn-danger:hover { background: rgba(255,59,87,0.12); filter: none; }
  details.form { border: 1px solid var(--line); border-radius: 7px; background: var(--panel); }
  details.form > summary { cursor: pointer; padding: 10px 14px; color: var(--ink); font-weight: 550; list-style: none; }
  details.form > summary::-webkit-details-marker { display: none; }
  details.form > summary::before { content: "+"; display: inline-block; width: 14px; color: var(--accent); font-family: var(--mono); }
  details.form[open] > summary::before { content: "-"; }
  details.form[open] > summary { border-bottom: 1px solid var(--line); }
  .ver { border: 1px solid var(--line); border-radius: 6px; padding: 9px 11px; margin-top: 7px; background: var(--panel-2); }
  .ver-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .kv { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 4px 12px; margin: 0; }
  .kv dt { color: var(--ink-3); font-size: 11.5px; }
  .kv dd { margin: 0; word-break: break-all; font-family: var(--mono); font-size: 12px; }
  .empty { padding: 22px 14px; text-align: center; color: var(--ink-3); }
  .test-out { margin-top: 6px; font-size: 12px; font-family: var(--mono); }
  .test-out.ok { color: var(--ok); }
  .test-out.bad { color: var(--bad); }
  .search { width: 210px; }
  .check { display: flex; align-items: center; gap: 7px; font-weight: 500; margin: 0; }
  .check input { width: auto; margin: 0; }
  .toolbar { display: flex; gap: 9px; align-items: center; flex-wrap: wrap; }
</style>
</head>
<body data-view="{{if .CreatedToken}}tokens{{end}}">
<a class="skip" href="#work">跳到主内容</a>
<div class="shell">
  <aside class="rail">
    <div class="brand">
      <b>底图网关</b>
      <span>Basemap Pusher</span>
    </div>
    <div class="nav-label">运维</div>
    <a class="nav-item" href="#overview" data-view-link="overview">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 12h4l2-7 2 11 2-6h4"/></svg>
      总览
    </a>
    <a class="nav-item" href="#basemaps" data-view-link="basemaps">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 5l7-2 7 2-7 2-7-2z"/><path d="M3 10l7 2 7-2"/><path d="M3 15l7 2 7-2"/></svg>
      底图<span class="n">{{len .Basemaps}}</span>
    </a>
    <a class="nav-item" href="#vectors" data-view-link="vectors">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 16V6l6-3 6 3v10"/><path d="M8 16v-5h4v5"/></svg>
      矢量<span class="n">{{len .VectorAssets}}</span>
    </a>
    <a class="nav-item" href="#tokens" data-view-link="tokens">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="7.5" cy="10" r="3.5"/><path d="M11 10h6"/><path d="M15 10v3"/></svg>
      令牌<span class="n">{{len .Tokens}}</span>
    </a>
    <a class="nav-item" href="#pushes" data-view-link="pushes">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M10 15V5"/><path d="M6 9l4-4 4 4"/><path d="M4 16h12"/></svg>
      推送<span class="n">{{len .TargetSystems}}</span>
    </a>
    <div class="nav-label">系统</div>
    <a class="nav-item" href="#scans" data-view-link="scans">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 5h12v10H4z"/><path d="M7 8h6"/><path d="M7 11h4"/></svg>
      扫描<span class="n">{{len .ScanSources}}</span>
    </a>
    <a class="nav-item" href="#tiles" data-view-link="tiles">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 4h5v5H4z"/><path d="M11 4h5v5h-5z"/><path d="M4 11h5v5H4z"/><path d="M11 11h5v5h-5z"/></svg>
      切片<span class="n">{{len .TileJobs}}</span>
    </a>
    <a class="nav-item" href="#system" data-view-link="system">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="10" cy="10" r="2.6"/><path d="M10 3v2M10 15v2M3 10h2M15 10h2M5 5l1.5 1.5M13.5 13.5L15 15M15 5l-1.5 1.5M6.5 13.5L5 15"/></svg>
      服务
    </a>
    <div class="rail-foot">{{.SystemInfo.ServiceName}} · v{{.SystemInfo.ServiceVersion}}</div>
  </aside>

  <div class="main">
    <header class="topbar">
      <h1 id="view-title">总览</h1>
      <span class="chip {{if .SystemInfo.Env}}is-ok{{end}}"><span class="dot"></span>{{if .SystemInfo.Env}}{{.SystemInfo.Env}}{{else}}未配置环境{{end}}</span>
      <span class="chip">启动 {{fmtTime .SystemInfo.StartedAt}}</span>
      <span class="spacer"></span>
      <span class="chip" id="metrics-chip"><span class="dot"></span><span id="metrics-chip-text">指标加载中</span></span>
    </header>
    <main class="views" id="work">
      {{if .Flash}}<div class="notice {{flashClass .Flash}}">{{.Flash}}</div>{{end}}
      {{if .CreatedToken}}
      <div class="mint">
        <h2>访问令牌已创建</h2>
        <p class="lead">明文仅显示这一次，服务端只保存哈希。请立即复制并交付给使用者。</p>
        <code>{{.CreatedToken}}</code>
        <div class="row-actions">
          <button type="button" data-copy="{{.CreatedToken}}">复制令牌</button>
          <span class="small muted" data-copy-feedback hidden>已复制到剪贴板</span>
        </div>
      </div>
      {{end}}

      <section class="view" id="view-overview" hidden>
        <div class="split">
          <div class="sec">
            <div class="sec-head">
              <h2>待处理</h2>
              <span class="count" id="attention-count"></span>
            </div>
            <div class="band">
              <ul class="attention" id="attention">
                {{range .ScanRuns}}{{if eq .Status "failed"}}
                <li><span class="badge danger">扫描失败</span><span class="what">{{.SourceCode}}</span><span class="why">{{index .Summary "error"}}</span><span class="at">{{fmtTime .StartedAt}}</span></li>
                {{end}}{{end}}
                {{range .PushRecords}}{{if eq .Status "failed"}}
                <li><span class="badge danger">推送失败</span><span class="what">{{.TargetSystemCode}}</span><span class="why">{{.BasemapCode}} / {{.BasemapVersion}}</span><span class="at">{{fmtTime .PushedAt}}</span></li>
                {{end}}{{end}}
                {{range .VectorPushRecords}}{{if eq .Status "failed"}}
                <li><span class="badge danger">推送失败</span><span class="what">{{.TargetSystemCode}}</span><span class="why">{{.VectorCode}}</span><span class="at">{{fmtTime .PushedAt}}</span></li>
                {{end}}{{end}}
                {{range .PushRecords}}{{if eq .Status "pending"}}
                <li><span class="badge warning">推送进行中</span><span class="what">{{.TargetSystemCode}}</span><span class="why">{{.BasemapCode}} / {{.BasemapVersion}}</span><span class="at">{{fmtTime .PushedAt}}</span></li>
                {{end}}{{end}}
                {{range .VectorPushRecords}}{{if eq .Status "pending"}}
                <li><span class="badge warning">推送进行中</span><span class="what">{{.TargetSystemCode}}</span><span class="why">{{.VectorCode}}</span><span class="at">{{fmtTime .PushedAt}}</span></li>
                {{end}}{{end}}
                {{range .Basemaps}}{{range .Versions}}{{$h := index $.VersionHealth .ID}}{{if and $h.Health (ne $h.Health "ok")}}
                <li><span class="badge danger">底图缺失</span><span class="what">{{.BasemapCode}} / {{.Version}}</span><span class="why">{{if $h.Detail}}{{$h.Detail}}{{else}}路径不可用{{end}}</span><span class="at">巡检</span></li>
                {{end}}{{end}}{{end}}
                {{range .VectorAssets}}{{$h := index $.VectorHealth .ID}}{{if and $h.Health (ne $h.Health "ok")}}
                <li><span class="badge danger">矢量缺失</span><span class="what">{{.Code}}</span><span class="why">{{if $h.Detail}}{{$h.Detail}}{{else}}文件不可用{{end}}</span><span class="at">巡检</span></li>
                {{end}}{{end}}
                <li class="attention-empty"><span class="why">没有失败的扫描或推送，资源也都在位。</span></li>
              </ul>
            </div>
            <div class="toolbar" style="margin-top:10px">
              <form method="post" action="/admin/health/check"><button type="submit" class="btn-ghost">立即巡检资源</button></form>
              <span class="small muted">按目录内容核对每个底图版本与矢量文件是否还在原位。</span>
            </div>
          </div>

          <div class="sec">
            <div class="sec-head"><h2>近期扫描</h2><span class="count">越靠右越新</span></div>
            <div class="band"><div class="band-body">
              <div class="scan">
                {{range .ScanRuns}}<i class="{{if eq .Status "failed"}}ramp-bad{{else if eq .Status "running"}}ramp-warn{{else}}ramp-ok{{end}}" title="{{.SourceCode}} · {{statusText .Status}} · 新增 {{.AddedCount}} 更新 {{.UpdatedCount}} 删除 {{.RemovedCount}} · {{fmtTime .StartedAt}}"></i>{{end}}
              </div>
              <div class="scan-scale"><span>最近 {{len .ScanRuns}} 次</span><span>现在</span></div>
              <div class="legend">
                <span><i style="background:var(--r-2)"></i>成功</span>
                <span><i style="background:var(--r-3)"></i>进行中</span>
                <span><i style="background:var(--r-5)"></i>失败</span>
              </div>
            </div></div>
          </div>
        </div>

        <div class="sec" style="margin-top:24px">
          <div class="sec-head">
            <h2>服务指标</h2>
            <span class="count">来自 /metrics，进程内计数</span>
            <span class="spacer"></span>
            <a class="small" href="/metrics" target="_blank" rel="noreferrer">查看原始输出</a>
          </div>
          <div class="metrics">
            <div class="metric"><b id="m-req">-</b><span>请求总数</span></div>
            <div class="metric"><b id="m-4xx">-</b><span>4xx 响应</span></div>
            <div class="metric"><b id="m-5xx">-</b><span>5xx 响应</span></div>
            <div class="metric"><b id="m-auth">-</b><span>鉴权失败</span></div>
            <div class="metric"><b id="m-tiles">-</b><span>瓦片请求</span></div>
            <div class="metric"><b id="m-miss">-</b><span>瓦片 404</span></div>
            <div class="metric"><b id="m-up">-</b><span>运行时长</span></div>
          </div>
          <p class="field-note">指标在服务重启后归零。</p>
        </div>
      </section>

      <section class="view" id="view-basemaps" hidden>
        <div class="sec">
          <div class="sec-head">
            <h2>底图台账</h2>
            <span class="count">{{len .Basemaps}} 个资源</span>
            <span class="spacer"></span>
            <input class="search" type="text" placeholder="筛查编码、名称、地址" data-table-filter="#basemaps-table">
          </div>
          <div class="band"><div class="t-wrap">
            <table id="basemaps-table">
              <thead><tr><th>资源</th><th>版本</th><th>默认</th><th>取图模板</th><th>验真</th></tr></thead>
              <tbody>
                {{range .Basemaps}}
                <tr data-filterable="true">
                  <td>
                    <b>{{.Code}}</b>
                    <div class="small muted">{{.Name}}</div>
                    <div class="small muted">{{if .Description}}{{.Description}}{{else}}未填写资源说明{{end}}</div>
                  </td>
                  <td>
                    {{range .Versions}}
                    <div class="ver">
                      <div class="ver-head">
                        <span class="badge {{statusTone .Status}}">{{versionLabel .}}</span>
                        {{$h := index $.VersionHealth .ID}}{{if and $h.Health (ne $h.Health "ok")}}<span class="badge danger">文件缺失</span>{{else if $h.Health}}<span class="badge success">在位</span>{{end}}
                        <span class="small muted">级别 {{.MinZoom}}-{{.MaxZoom}}</span>
                        <span class="small muted">{{.TileFormat}}</span>
                      </div>
                      <div class="mono small break" style="margin-top:6px">{{.URLTemplate}}</div>
                      <div class="row-actions" style="margin-top:6px">
                        <button type="button" class="btn-quiet" data-copy="{{.URLTemplate}}">复制</button>
                        <span class="small muted" data-copy-feedback hidden>已复制</span>
                      </div>
                    </div>
                    {{end}}
                  </td>
                  <td class="nowrap">
                    <span class="mono">{{defaultVersion .Default}}</span>
                    {{if .Versions}}
                    <form method="post" action="/admin/basemaps/{{.Code}}/default" class="stack" style="margin-top:8px">
                      <select name="version">{{range .Versions}}<option value="{{.Version}}">{{.Version}}</option>{{end}}</select>
                      <button type="submit" class="btn-ghost">设为默认</button>
                    </form>
                    {{end}}
                  </td>
                  <td>
                    {{if .Default}}
                    <div class="mono small break">{{.Default.URLTemplate}}</div>
                    <div class="row-actions" style="margin-top:6px">
                      <button type="button" class="btn-quiet" data-copy="{{.Default.URLTemplate}}">复制</button>
                      <span class="small muted" data-copy-feedback hidden>已复制</span>
                    </div>
                    {{else}}<span class="muted">-</span>{{end}}
                  </td>
                  <td class="nowrap">
                    {{if .Default}}
                    <div class="stack">
                      <input type="text" placeholder="粘贴访问令牌" data-probe-token>
                      <button type="button" class="btn-ghost" data-probe-url="{{.Default.URLTemplate}}" data-probe-kind="tile">试拉一张</button>
                      <span class="test-out" data-probe-out hidden></span>
                    </div>
                    {{else}}<span class="muted">-</span>{{end}}
                  </td>
                </tr>
                {{else}}
                <tr><td colspan="5"><div class="empty">还没有底图。先到「扫描」里执行一次目录扫描。</div></td></tr>
                {{end}}
                <tr class="filter-empty" hidden><td colspan="5"><div class="empty">没有匹配的资源。</div></td></tr>
              </tbody>
            </table>
          </div></div>
        </div>
      </section>

      <section class="view" id="view-vectors" hidden>
        <div class="sec">
          <div class="sec-head">
            <h2>矢量资源</h2>
            <span class="count">{{len .VectorAssets}} 项</span>
            <span class="spacer"></span>
            <div class="toolbar">
              <input class="search" type="text" placeholder="筛查编码、路径" data-table-filter="#vectors-table">
              <form method="post" action="/admin/vectors/rescan"><button type="submit" class="btn-ghost">重新扫描</button></form>
            </div>
          </div>
          <p class="field-note" style="margin:-6px 0 12px">扫描根目录：<span class="mono break">{{.SystemInfo.VectorScanRoot}}</span></p>
          <div class="band"><div class="t-wrap">
            <table id="vectors-table">
              <thead><tr><th>资源</th><th>格式</th><th>大小</th><th>下载地址</th><th>验真</th></tr></thead>
              <tbody>
                {{range .VectorAssets}}
                <tr data-filterable="true">
                  <td>
                    <b>{{.Code}}</b>
                    <div class="small muted">{{.Name}}</div>
                    <div class="mono small break">{{vectorPath .}}</div>
                  </td>
                  <td>
                    <span class="badge neutral">{{.Format}}</span>
                    {{$h := index $.VectorHealth .ID}}{{if and $h.Health (ne $h.Health "ok")}}<div style="margin-top:5px"><span class="badge danger">文件缺失</span></div>{{else if $h.Health}}<div style="margin-top:5px"><span class="badge success">在位</span></div>{{end}}
                    <div class="small muted" style="margin-top:5px">{{statusText .Status}}</div>
                  </td>
                  <td class="small nowrap">{{formatBytes .FileSize}}</td>
                  <td>
                    <div class="mono small break">{{.DownloadURL}}</div>
                    <div class="row-actions" style="margin-top:6px">
                      <button type="button" class="btn-quiet" data-copy="{{.DownloadURL}}">复制</button>
                      <span class="small muted" data-copy-feedback hidden>已复制</span>
                    </div>
                  </td>
                  <td class="nowrap">
                    <div class="stack">
                      <input type="text" placeholder="粘贴访问令牌" data-probe-token>
                      <button type="button" class="btn-ghost" data-probe-url="{{.DownloadURL}}" data-probe-kind="vector">试下载</button>
                      <span class="test-out" data-probe-out hidden></span>
                    </div>
                  </td>
                </tr>
                {{else}}
                <tr><td colspan="5"><div class="empty">还没有矢量资源。把 GeoJSON 或 shapefile 放进扫描根目录后重新扫描。</div></td></tr>
                {{end}}
                <tr class="filter-empty" hidden><td colspan="5"><div class="empty">没有匹配的资源。</div></td></tr>
              </tbody>
            </table>
          </div></div>
        </div>
      </section>
      <section class="view" id="view-tokens" hidden>
        <div class="sec">
          <details class="form" {{if .CreatedToken}}open{{end}}>
            <summary>签发新令牌</summary>
            <div class="band-body">
              <form class="stack" method="post" action="/admin/tokens">
                <div class="grid-form">
                  <div><label>令牌名称</label><input name="name" placeholder="viewer-a" required></div>
                  <div><label>有效时长（分钟）</label><input name="expires_at" inputmode="numeric" placeholder="留空使用默认有效期"><div class="field-note">留空走服务端默认值。</div></div>
                  <div><label>附加能力</label><label class="check"><input type="checkbox" name="scopes" value="slice"> 允许提交切片任务</label><div class="field-note">不勾选则只能读取已授权的底图与矢量。</div></div>
                </div>
                <div class="grid-form">
                  <div><label>底图编码</label><textarea name="basemap_codes" placeholder="gaode-image, google-image"></textarea><div class="field-note">逗号或换行分隔，写底图的 code。</div></div>
                  <div><label>矢量资源</label><textarea name="vector_codes" placeholder="全国行政区.geojson, landuse/parcel.shp"></textarea><div class="field-note">可写 code、相对路径或文件名。</div></div>
                </div>
                <div><button type="submit">签发令牌</button></div>
              </form>
            </div>
          </details>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>已签发</h2><span class="count">{{len .Tokens}} 枚</span><span class="spacer"></span><input class="search" type="text" placeholder="筛查名称、前缀、授权" data-table-filter="#tokens-table"></div>
          <div class="band"><div class="t-wrap">
            <table id="tokens-table">
              <thead><tr><th>令牌</th><th>授权范围</th><th>状态</th><th>有效期</th><th></th></tr></thead>
              <tbody>
                {{range .Tokens}}
                <tr data-filterable="true">
                  <td>
                    <b>{{.Name}}</b>
                    <div class="mono small">{{.TokenPrefix}}</div>
                    <div class="small muted">创建 {{fmtTime .CreatedAt}}</div>
                  </td>
                  <td class="small">
                    <div><span class="muted">底图：</span>{{tokenGrants .Grants}}</div>
                    <div style="margin-top:4px"><span class="muted">矢量：</span>{{vectorTokenGrants .VectorGrants}}</div>
                    <div style="margin-top:4px"><span class="muted">能力：</span>{{if .Scopes}}{{range $i, $s := .Scopes}}{{if $i}} · {{end}}{{$s}}{{end}}{{else}}-{{end}}</div>
                  </td>
                  <td><span class="badge {{statusTone .Status}}">{{statusText .Status}}</span></td>
                  <td class="small nowrap">
                    <div>到期 {{fmtTimePtr .ExpiresAt}}</div>
                    <div class="muted">最近使用 {{fmtTimePtr .LastUsedAt}}</div>
                  </td>
                  <td>
                    <form class="inline" method="post" action="/admin/tokens/{{.ID}}/disable" data-confirm="停用后该令牌立即失效，正在用它取图的下游会开始收到 401。确认停用？">
                      <button type="submit" class="btn-danger">停用</button>
                    </form>
                  </td>
                </tr>
                {{else}}
                <tr><td colspan="5"><div class="empty">还没有令牌。签发一枚才能对外提供取图地址。</div></td></tr>
                {{end}}
                <tr class="filter-empty" hidden><td colspan="5"><div class="empty">没有匹配的令牌。</div></td></tr>
              </tbody>
            </table>
          </div></div>
        </div>
      </section>

      <section class="view" id="view-pushes" hidden>
        <div class="split">
          <div class="sec">
            <details class="form">
              <summary>推送底图</summary>
              <div class="band-body">
                <form class="stack" method="post" action="/admin/pushes">
                  <div class="grid-form">
                    <div><label>目标系统编码</label><input name="target_code" placeholder="portal-a" required></div>
                    <div><label>底图编码</label><input name="basemap_code" placeholder="gaode-image" required></div>
                    <div><label>版本</label><input name="version" value="current" required></div>
                  </div>
                  <div><button type="submit">提交推送</button></div>
                  <div class="field-note">推送在后台执行，提交后回到这里刷新查看结果。</div>
                </form>
              </div>
            </details>
          </div>
          <div class="sec">
            <details class="form">
              <summary>推送矢量</summary>
              <div class="band-body">
                <form class="stack" method="post" action="/admin/vector-pushes">
                  <div class="grid-form">
                    <div><label>目标系统编码</label><input name="target_code" placeholder="portal-a" required></div>
                    <div><label>矢量资源编码</label><input name="vector_code" placeholder="admin-boundary" required></div>
                  </div>
                  <div><button type="submit">提交推送</button></div>
                </form>
              </div>
            </details>
          </div>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>目标系统</h2><span class="count">{{len .TargetSystems}} 个</span></div>
          <div class="band">
            <div class="band-body" style="border-bottom:1px solid var(--line)">
              <form class="stack" method="post" action="/admin/targets">
                <div class="grid-form">
                  <div><label>系统编码</label><input name="code" placeholder="portal-a" required></div>
                  <div><label>系统名称</label><input name="name" placeholder="业务门户 A"></div>
                  <div><label>回调地址</label><input name="callback_url" placeholder="http://target/api/catalog" required></div>
                  <div><label>请求方法</label><input name="callback_method" value="POST"></div>
                </div>
                <div><label>附加请求头</label><textarea name="headers" placeholder="X-App-Key: abc123"></textarea><div class="field-note">每行一条 <span class="mono">名字: 值</span>。</div></div>
                <div><button type="submit">保存目标系统</button></div>
              </form>
            </div>
            <div class="t-wrap">
              <table>
                <thead><tr><th>系统</th><th>回调</th><th>状态</th></tr></thead>
                <tbody>
                  {{range .TargetSystems}}
                  <tr>
                    <td><b>{{.Code}}</b><div class="small muted">{{.Name}}</div></td>
                    <td>
                      <div class="small muted">{{.CallbackMethod}}</div>
                      <div class="mono small break">{{.CallbackURL}}</div>
                      <div class="row-actions" style="margin-top:6px">
                        <button type="button" class="btn-quiet" data-copy="{{.CallbackURL}}">复制</button>
                        <span class="small muted" data-copy-feedback hidden>已复制</span>
                      </div>
                    </td>
                    <td><span class="badge {{enabledTone .Enabled}}">{{enabledText .Enabled}}</span></td>
                  </tr>
                  {{else}}
                  <tr><td colspan="3"><div class="empty">还没有目标系统。</div></td></tr>
                  {{end}}
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>推送记录</h2><span class="count">最近 10 条</span></div>
          <div class="band"><div class="t-wrap">
            <table>
              <thead><tr><th>类型</th><th>目标</th><th>资源</th><th>状态</th><th>时间</th><th></th></tr></thead>
              <tbody>
                {{range .PushRecords}}
                <tr>
                  <td><span class="badge neutral">底图</span></td>
                  <td>{{.TargetSystemCode}}</td>
                  <td class="small">{{.BasemapCode}} / {{.BasemapVersion}}</td>
                  <td>
                    <span class="badge {{statusTone .Status}}">{{statusText .Status}}</span>
                    {{if gt .AttemptCount 0}}<div class="small muted" style="margin-top:4px">已尝试 {{.AttemptCount}} 次</div>{{end}}
                    {{if .NextAttemptAt}}<div class="small muted">下次 {{fmtTimePtr .NextAttemptAt}}</div>{{end}}
                    {{if .ErrorMessage}}<div class="small muted break" style="margin-top:4px">{{.ErrorMessage}}</div>{{end}}
                  </td>
                  <td class="small nowrap">{{fmtTime .PushedAt}}</td>
                  <td>
                    {{if or (eq .Status "failed") (eq .Status "retrying")}}
                    <form class="inline" method="post" action="/admin/push-records/{{.ID}}/retry"><button type="submit" class="btn-ghost">重推</button></form>
                    {{end}}
                  </td>
                </tr>
                {{end}}
                {{range .VectorPushRecords}}
                <tr>
                  <td><span class="badge neutral">矢量</span></td>
                  <td>{{.TargetSystemCode}}</td>
                  <td class="small">{{.VectorCode}}</td>
                  <td>
                    <span class="badge {{statusTone .Status}}">{{statusText .Status}}</span>
                    {{if gt .AttemptCount 0}}<div class="small muted" style="margin-top:4px">已尝试 {{.AttemptCount}} 次</div>{{end}}
                    {{if .NextAttemptAt}}<div class="small muted">下次 {{fmtTimePtr .NextAttemptAt}}</div>{{end}}
                    {{if .ErrorMessage}}<div class="small muted break" style="margin-top:4px">{{.ErrorMessage}}</div>{{end}}
                  </td>
                  <td class="small nowrap">{{fmtTime .PushedAt}}</td>
                  <td>
                    {{if or (eq .Status "failed") (eq .Status "retrying")}}
                    <form class="inline" method="post" action="/admin/vector-push-records/{{.ID}}/retry"><button type="submit" class="btn-ghost">重推</button></form>
                    {{end}}
                  </td>
                </tr>
                {{end}}
                {{if and (eq (len .PushRecords) 0) (eq (len .VectorPushRecords) 0)}}
                <tr><td colspan="6"><div class="empty">还没有推送记录。</div></td></tr>
                {{end}}
              </tbody>
            </table>
          </div></div>
        </div>
      </section>
      <section class="view" id="view-scans" hidden>
        <div class="sec">
          <details class="form" {{if eq (len .ScanSources) 0}}open{{end}}>
            <summary>新增扫描源</summary>
            <div class="band-body">
              <form class="stack" method="post" action="/admin/scan-sources">
                <div class="grid-form">
                  <div><label>扫描源编码</label><input name="code" value="local-basemaps" required></div>
                  <div><label>扫描源名称</label><input name="name" value="本地底图目录"></div>
                  <div><label>根目录路径</label><input name="root_path" value="{{.SystemInfo.ScanRoot}}" required></div>
                  <div><label>清单文件名</label><input name="manifest_name" value="manifest.json"></div>
                </div>
                <div><button type="submit">保存扫描源</button></div>
              </form>
            </div>
          </details>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>扫描源</h2><span class="count">{{len .ScanSources}} 个</span></div>
          <div class="band"><div class="t-wrap">
            <table>
              <thead><tr><th>来源</th><th>根目录</th><th>状态</th><th>操作</th></tr></thead>
              <tbody>
                {{range .ScanSources}}
                <tr>
                  <td><b>{{.Code}}</b><div class="small muted">{{.Name}}</div></td>
                  <td>
                    <div class="mono small break">{{.RootPath}}</div>
                    <div class="small muted">清单文件：{{.ManifestName}}</div>
                  </td>
                  <td><span class="badge {{enabledTone .Enabled}}">{{enabledText .Enabled}}</span></td>
                  <td>
                    <form class="inline" method="post" action="/admin/scan-sources/{{.Code}}/run" data-confirm="扫描会按目录内容重建这个来源下的版本台账。确认执行？">
                      <button type="submit" class="btn-ghost">立即扫描</button>
                    </form>
                  </td>
                </tr>
                {{else}}
                <tr><td colspan="4"><div class="empty">还没有扫描源。</div></td></tr>
                {{end}}
              </tbody>
            </table>
          </div></div>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>扫描记录</h2><span class="count">最近 10 次</span></div>
          <div class="band">
            <div class="band-body" style="border-bottom:1px solid var(--line)">
              <div class="scan">
                {{range .ScanRuns}}<i class="{{if eq .Status "failed"}}ramp-bad{{else if eq .Status "running"}}ramp-warn{{else}}ramp-ok{{end}}"></i>{{end}}
              </div>
              <div class="scan-scale"><span>更早</span><span>现在</span></div>
            </div>
            <div class="t-wrap">
              <table>
                <thead><tr><th>来源</th><th>变更</th><th>状态</th><th>开始时间</th></tr></thead>
                <tbody>
                  {{range .ScanRuns}}
                  <tr>
                    <td><b>{{.SourceCode}}</b></td>
                    <td class="small">新增 {{.AddedCount}} · 更新 {{.UpdatedCount}} · 删除 {{.RemovedCount}}{{if index .Summary "unchanged_count"}} · 未变 {{index .Summary "unchanged_count"}}{{end}}</td>
                    <td><span class="badge {{statusTone .Status}}">{{statusText .Status}}</span>{{if index .Summary "error"}}<div class="small muted break" style="margin-top:4px">{{index .Summary "error"}}</div>{{end}}</td>
                    <td class="small nowrap">{{fmtTime .StartedAt}}</td>
                  </tr>
                  {{else}}
                  <tr><td colspan="4"><div class="empty">还没有扫描记录。</div></td></tr>
                  {{end}}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </section>

      <section class="view" id="view-tiles" hidden>
        <div class="sec">
          <details class="form">
            <summary>提交切片任务</summary>
            <div class="band-body">
              <form class="stack" method="post" action="/admin/tile-jobs">
                <div class="grid-form">
                  <div><label>源影像路径</label><input name="source_path" placeholder="D:\data\input\image.tif" required></div>
                  <div><label>目标底图编码</label><input name="target_code" placeholder="gaode-2026" required><div class="field-note">切片会落到扫描根目录下的这个子目录。</div></div>
                </div>
                <div class="grid-form">
                  <div><label>最小级别</label><input name="zoom_min" inputmode="numeric" value="0"></div>
                  <div><label>最大级别</label><input name="zoom_max" inputmode="numeric" value="12"></div>
                  <div><label>瓦片尺寸</label><input name="tile_size" inputmode="numeric" value="256"></div>
                  <div><label>瓦片格式</label><input name="tile_format" value="webp"></div>
                </div>
                <div class="grid-form">
                  <div><label>任务编码</label><input name="code" placeholder="留空自动生成"></div>
                  <div><label>任务名称</label><input name="name" placeholder="留空取文件名"></div>
                  <div><label>申请人</label><input name="requested_by" placeholder="ops"></div>
                  <div><label>完成回调地址</label><input name="callback_url" placeholder="http://caller/api/tile-jobs/done"><div class="field-note">可选，留空则只写记录。</div></div>
                </div>
                <div><button type="submit">提交切片</button></div>
                <div class="field-note">
                  实际执行命令：<span class="mono break">{{if .SystemInfo.TileJobCommand}}{{.SystemInfo.TileJobCommand}}{{else}}gdal2tiles.py --xyz --zoom=&#123;zmin&#125;-&#123;zmax&#125; --tilesize=&#123;tilesize&#125; &#123;input&#125; &#123;output&#125;{{end}}</span>
                </div>
              </form>
            </div>
          </details>
        </div>

        <div class="sec">
          <div class="sec-head"><h2>切片任务</h2><span class="count">最近 10 个</span></div>
          <div class="band"><div class="t-wrap">
            <table>
              <thead><tr><th>任务</th><th>源影像</th><th>级别</th><th>状态</th><th>提交时间</th></tr></thead>
              <tbody>
                {{range .TileJobs}}
                <tr>
                  <td>
                    <b>{{.Code}}</b>
                    <div class="small muted">{{.Name}}</div>
                    <div class="small muted">目标底图：{{.TargetCode}}{{if gt .SourceCount 1}} · {{.SourceCount}} 源镶嵌{{else}} · 单源{{end}}</div>
                    {{if gt (len .SourcePaths) 1}}<div class="mono small break">{{range $i, $s := .SourcePaths}}{{if $i}}<br>{{end}}{{$s}}{{end}}</div>{{end}}
                  </td>
                  <td class="mono small break">{{.SourcePath}}</td>
                  <td class="small nowrap">{{.ZoomMin}}-{{.ZoomMax}} · {{.TileSize}}px · {{.TileFormat}}</td>
                  <td>
                    <span class="badge {{statusTone .Status}}">{{statusText .Status}}</span>
                    {{if .Message}}<div class="small muted break" style="margin-top:4px">{{.Message}}</div>{{end}}
                    {{if .CallbackURL}}<div class="small muted break" style="margin-top:4px">回调：{{.CallbackURL}}{{if .CallbackStatus}} · {{statusText .CallbackStatus}}{{if .CallbackAttempts}}（已尝试 {{.CallbackAttempts}} 次）{{end}}{{end}}</div>{{end}}
                    {{if .CallbackMessage}}<div class="small muted break">{{.CallbackMessage}}</div>{{end}}
                    {{if .LogPath}}<div class="small muted break" style="margin-top:4px">日志：{{.LogPath}}</div>{{end}}
                  </td>
                  <td class="small nowrap">{{fmtTime .CreatedAt}}</td>
                </tr>
                {{else}}
                <tr><td colspan="5"><div class="empty">还没有切片任务。提交一个会把影像切成 z/x/y 瓦片并自动扫描入库。</div></td></tr>
                {{end}}
              </tbody>
            </table>
          </div></div>
        </div>
        <div class="sec">
          <div class="sec-head"><h2>图层源清单</h2><span class="count">{{len .Layers}} 个图层</span></div>
          <p class="field-note" style="margin:-6px 0 12px">同一个图层可以持续累加影像：日常只提交新飞的影像；想把整个图层重建成无接缝镶嵌，就把下面列出的路径一起提交。</p>
          <div class="band"><div class="t-wrap">
            <table>
              <thead><tr><th>图层</th><th>源影像</th><th>怎么用</th></tr></thead>
              <tbody>
                {{range .Layers}}
                <tr>
                  <td>
                    <b>{{.TargetCode}}</b>
                    <div class="small muted">{{len .Sources}} 张影像</div>
                    <div class="small muted">渲染名由调用端决定</div>
                  </td>
                  <td>
                    {{range .Sources}}
                    <div class="mono small break">{{.SourcePath}}<span class="muted"> · {{fmtTime .AddedAt}}</span></div>
                    {{end}}
                  </td>
                  <td class="small muted">
                    {{if gt (len .Sources) 1}}镶嵌重建：走 VRT 逐像素拼接，无瓦片接缝，级别 {{.ZoomMin}}-{{.ZoomMax}}。<br>{{else}}目前只有 1 张影像，重建等同于按原级别重切一次（{{.ZoomMin}}-{{.ZoomMax}}）。<br>{{end}}
                    日常增量：只提交新飞的影像，新影像覆盖到哪算哪。
                    <div class="row-actions" style="margin-top:8px">
                      <form method="post" action="/admin/tile-jobs/rebuild" data-confirm="会用该图层的 {{len .Sources}} 张影像重建整个瓦片目录，级别 {{.ZoomMin}}-{{.ZoomMax}}，格式 {{.TileFormat}}。任务执行期间旧瓦片会被覆盖。确认提交？">
                        <input type="hidden" name="target_code" value="{{.TargetCode}}">
                        <button type="submit" class="btn-ghost">重建为镶嵌</button>
                      </form>
                    </div>
                  </td>
                </tr>
                {{else}}
                <tr><td colspan="3"><div class="empty">还没有图层源记录。提交切片任务后，这里会列出每个图层由哪些影像构成。</div></td></tr>
                {{end}}
              </tbody>
            </table>
          </div></div>
        </div>
      </section>
      <section class="view" id="view-system" hidden>
        <div class="split">
          <div class="sec">
            <div class="sec-head"><h2>服务</h2></div>
            <div class="band"><div class="band-body">
              <dl class="kv">
                <dt>服务名</dt><dd>{{.SystemInfo.ServiceName}}</dd>
                <dt>版本</dt><dd>{{.SystemInfo.ServiceVersion}}</dd>
                <dt>提交</dt><dd>{{if .SystemInfo.Commit}}{{.SystemInfo.Commit}}{{else}}-{{end}}</dd>
                <dt>环境</dt><dd>{{if .SystemInfo.Env}}{{.SystemInfo.Env}}{{else}}未配置{{end}}</dd>
                <dt>启动时间</dt><dd>{{fmtTime .SystemInfo.StartedAt}}</dd>
              </dl>
            </div></div>
          </div>
          <div class="sec">
            <div class="sec-head"><h2>对外地址</h2></div>
            <div class="band"><div class="band-body">
              <dl class="kv">
                <dt>API</dt><dd>{{.SystemInfo.APIBaseURL}}</dd>
                <dt>瓦片</dt><dd>{{.SystemInfo.TileBaseURL}}</dd>
                <dt>矢量</dt><dd>{{.SystemInfo.VectorBaseURL}}</dd>
                <dt>SQLite</dt><dd>{{.SystemInfo.SQLitePath}}</dd>
                <dt>底图根目录</dt><dd>{{.SystemInfo.ScanRoot}}</dd>
                <dt>矢量根目录</dt><dd>{{.SystemInfo.VectorScanRoot}}</dd>
              </dl>
            </div></div>
          </div>
        </div>
        <div class="sec">
          <div class="sec-head"><h2>端点</h2></div>
          <div class="band"><div class="band-body">
            <div class="legend">
              <span><a href="/health" target="_blank" rel="noreferrer">/health</a> 存活探针</span>
              <span><a href="/ready" target="_blank" rel="noreferrer">/ready</a> 就绪探针</span>
              <span><a href="/metrics" target="_blank" rel="noreferrer">/metrics</a> Prometheus 指标</span>
              <span><a href="/api/v1/system" target="_blank" rel="noreferrer">/api/v1/system</a> 服务信息</span>
            </div>
          </div></div>
        </div>
      </section>
    </main>
  </div>
</div>
<script>
(function () {
  "use strict";

  var TITLES = {
    overview: "总览", basemaps: "底图台账", vectors: "矢量资源", tokens: "访问令牌",
    pushes: "推送", scans: "扫描", tiles: "影像切片", system: "服务"
  };
  var VIEWS = Object.keys(TITLES);

  function currentView() {
    var id = (window.location.hash || "").replace(/^#/, "");
    return VIEWS.indexOf(id) === -1 ? "overview" : id;
  }

  function show(view, animate) {
    VIEWS.forEach(function (name) {
      var section = document.getElementById("view-" + name);
      if (section) section.hidden = name !== view;
    });
    Array.prototype.slice.call(document.querySelectorAll("[data-view-link]")).forEach(function (link) {
      if (link.getAttribute("data-view-link") === view) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
    var title = document.getElementById("view-title");
    if (title) title.textContent = TITLES[view];
    if (animate) {
      var section = document.getElementById("view-" + view);
      if (section) {
        section.classList.remove("sweep");
        void section.offsetWidth;
        section.classList.add("sweep");
      }
    }
    document.title = TITLES[view] + " · 底图网关";
  }

  window.addEventListener("hashchange", function () { show(currentView(), true); });
  show(currentView(), false);

  var initial = document.body.getAttribute("data-view");
  if (initial && VIEWS.indexOf(initial) !== -1 && currentView() !== initial) {
    window.location.hash = initial;
    show(initial, true);
  }

  var attention = document.getElementById("attention");
  if (attention) {
    var real = attention.querySelectorAll("li:not(.attention-empty)");
    var emptyRow = attention.querySelector(".attention-empty");
    if (emptyRow) emptyRow.hidden = real.length > 0;
    var counter = document.getElementById("attention-count");
    if (counter) {
      counter.textContent = real.length === 0 ? "无" : real.length + " 项需要处理";
      counter.style.color = real.length === 0 ? "" : "var(--bad)";
    }
  }

  function fallbackCopy(text) {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "readonly");
    area.style.position = "absolute";
    area.style.left = "-9999px";
    document.body.appendChild(area);
    area.select();
    document.execCommand("copy");
    document.body.removeChild(area);
  }

  Array.prototype.slice.call(document.querySelectorAll("[data-copy]")).forEach(function (button) {
    var original = button.textContent;
    button.addEventListener("click", function () {
      var text = button.getAttribute("data-copy") || "";
      var feedback = button.parentElement ? button.parentElement.querySelector("[data-copy-feedback]") : null;
      Promise.resolve()
        .then(function () {
          if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text);
          fallbackCopy(text);
        })
        .then(function () {
          button.textContent = "已复制";
          if (feedback) feedback.hidden = false;
          window.setTimeout(function () {
            button.textContent = original;
            if (feedback) feedback.hidden = true;
          }, 1400);
        })
        .catch(function () {
          button.textContent = "复制失败";
          window.setTimeout(function () { button.textContent = original; }, 1400);
        });
    });
  });

  function applyFilter(input) {
    var table = document.querySelector(input.getAttribute("data-table-filter"));
    if (!table) return;
    var rows = Array.prototype.slice.call(table.querySelectorAll("tbody tr[data-filterable='true']"));
    var emptyRow = table.querySelector("tbody .filter-empty");
    var keyword = input.value.trim().toLowerCase();
    var visible = 0;
    rows.forEach(function (row) {
      var match = keyword === "" || (row.textContent || "").toLowerCase().indexOf(keyword) !== -1;
      row.hidden = !match;
      if (match) visible += 1;
    });
    if (emptyRow) emptyRow.hidden = !(rows.length > 0 && keyword !== "" && visible === 0);
  }

  Array.prototype.slice.call(document.querySelectorAll("[data-table-filter]")).forEach(function (input) {
    input.addEventListener("input", function () { applyFilter(input); });
    applyFilter(input);
  });

  Array.prototype.slice.call(document.querySelectorAll("[data-confirm]")).forEach(function (form) {
    form.addEventListener("submit", function (event) {
      if (!window.confirm(form.getAttribute("data-confirm"))) event.preventDefault();
    });
  });

  function probe(button) {
    var cell = button.closest("td") || button.parentElement;
    var tokenInput = cell ? cell.querySelector("[data-probe-token]") : null;
    var out = cell ? cell.querySelector("[data-probe-out]") : null;
    var template = button.getAttribute("data-probe-url") || "";
    var kind = button.getAttribute("data-probe-kind");
    var token = tokenInput ? tokenInput.value.trim() : "";

    if (!out) return;
    out.hidden = false;
    out.className = "test-out";
    if (!template) { out.textContent = "没有可用的地址"; return; }
    if (!token) { out.className = "test-out bad"; out.textContent = "先填写访问令牌"; return; }

    var url = template.replace("{z}", "0").replace("{x}", "0").replace("{y}", "0");
    var started = Date.now();
    out.textContent = "请求中…";

    fetch(url, { headers: { Authorization: "Bearer " + token } })
      .then(function (response) {
        var ms = Date.now() - started;
        out.className = "test-out " + (response.ok ? "ok" : "bad");
        var label = kind === "vector" ? "下载" : "取图";
        out.textContent = label + (response.ok ? "成功 " : "失败 ") + response.status + " · " + ms + "ms" +
          (response.status === 401 ? " · 令牌无效或过期" : "") +
          (response.status === 403 ? " · 令牌没有这个资源的权限" : "") +
          (response.status === 404 ? " · 地址不存在或资源已下线" : "");
      })
      .catch(function (err) {
        out.className = "test-out bad";
        out.textContent = "请求失败：" + err.message;
      });
  }

  Array.prototype.slice.call(document.querySelectorAll("[data-probe-url]")).forEach(function (button) {
    button.addEventListener("click", function () { probe(button); });
  });

  function readMetric(text, name) {
    var pattern = "^" + name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s+([0-9.eE+-]+)";
    var match = text.match(new RegExp(pattern, "m"));
    return match ? Number(match[1]) : null;
  }

  function fmtDuration(seconds) {
    if (seconds === null || isNaN(seconds)) return "-";
    var s = Math.floor(seconds);
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + m + "m";
    return m + "m";
  }

  function setMetric(id, value) {
    var el = document.getElementById(id);
    if (el && value !== null) el.textContent = value;
  }

  fetch("/metrics")
    .then(function (response) { return response.text(); })
    .then(function (text) {
      var four = readMetric(text, "basemap_pusher_requests_by_status_total{class=\"4xx\"}");
      var five = readMetric(text, "basemap_pusher_requests_by_status_total{class=\"5xx\"}");
      var auth = readMetric(text, "basemap_pusher_auth_failures_total");
      var tiles = readMetric(text, "basemap_pusher_tile_requests_total");
      var miss = readMetric(text, "basemap_pusher_tile_misses_total");
      var up = readMetric(text, "basemap_pusher_uptime_seconds");

      setMetric("m-req", readMetric(text, "basemap_pusher_requests_total"));
      setMetric("m-4xx", four);
      setMetric("m-5xx", five);
      setMetric("m-auth", auth);
      setMetric("m-tiles", tiles);
      setMetric("m-miss", miss);
      setMetric("m-up", fmtDuration(up));

      var fiveCard = document.getElementById("m-5xx");
      if (fiveCard && five > 0) fiveCard.parentElement.classList.add("is-bad");
      var missCard = document.getElementById("m-miss");
      if (missCard && tiles > 0) {
        var rate = miss / tiles;
        if (rate > 0.25) missCard.parentElement.classList.add("is-bad");
        else if (rate > 0.05) missCard.parentElement.classList.add("is-warn");
      }
      var chip = document.getElementById("metrics-chip");
      var chipText = document.getElementById("metrics-chip-text");
      if (chip && chipText) {
        chipText.textContent = "瓦片 404 " + (miss === null ? "-" : miss) + " / " + (tiles === null ? "-" : tiles);
        chip.classList.add(five > 0 ? "is-bad" : "is-ok");
      }
    })
    .catch(function () {
      var chipText = document.getElementById("metrics-chip-text");
      if (chipText) chipText.textContent = "指标不可用";
    });
})();
</script>
</body>
</html>`
