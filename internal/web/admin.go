// 管理端：库内数据的浏览/修正，以及"指定文件重新解析并覆盖"。
//
// 设计上有三条硬约束，改动前先读：
//  1. 写接口一律 POST/DELETE，且被 adminGuard 拦住——配了 token 就查 token，
//     没配 token 就只放行本机回环地址。这是为了不把"改库"能力暴露到局域网。
//  2. 同一时刻只允许一个写任务（重载/清理），用 job 单飞 + 轮询进度实现；
//     SQLite 侧 SetMaxOpenConns(1) 本来就串行化写入，这里再挡一层逻辑互踩。
//  3. 覆盖语义依赖 wz_file.ids 溯源；旧库该列为空时只能覆盖解析到的字段，
//     无法反查并清除历史贡献（比对接口会如实报出这个状态）。
package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/config"
	"github.com/sqx6781268/MapleWzMeta/internal/scan"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
)

// adminJob 是一次后台写任务。指针字段只在 s.mu 保护下读写。
type adminJob struct {
	ID         int64      `json:"id"`
	Lang       string     `json:"lang"`
	Kind       string     `json:"kind"` // reload / prune
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Target     int        `json:"target"` // 本次要重解析的文件数
	Note       string     `json:"note,omitempty"`
	Err        string     `json:"err,omitempty"`
	Summary    *jobStat   `json:"summary,omitempty"`
	Missing    []string   `json:"missing,omitempty"`
}

// jobStat 是任务产出的统计，直接取自 scan.Result 的对外视图。
type jobStat struct {
	Parsed       int   `json:"parsed"`
	Failed       int   `json:"failed"`
	Names        int   `json:"names"`
	Infos        int   `json:"infos"`
	Cleared      int   `json:"cleared"`
	Protected    int   `json:"protected"`
	DeletedFiles int   `json:"deletedFiles"`
	DurationMSec int64 `json:"durationMs"`
}

func (j *adminJob) snapshot() *adminJob {
	c := *j
	c.FinishedAt = j.FinishedAt
	if j.Summary != nil {
		s := *j.Summary
		c.Summary = &s
	}
	c.Missing = append([]string(nil), j.Missing...)
	return &c
}

// Handler 里注册管理路由；返回的 handler 已含鉴权与开关判断。
func (s *Server) adminRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/diff", s.handleAdminDiff)
	mux.HandleFunc("GET /api/admin/files", s.handleAdminFiles)
	mux.HandleFunc("GET /api/admin/runs", s.handleAdminRuns)
	mux.HandleFunc("GET /api/admin/job", s.handleAdminJob)
	mux.HandleFunc("GET /api/admin/sources", s.handleAdminSources)
	mux.HandleFunc("POST /api/admin/reload", s.handleAdminReload)
	mux.HandleFunc("POST /api/admin/prune", s.handleAdminPrune)
	mux.HandleFunc("POST /api/admin/item", s.handleAdminItem)
	mux.HandleFunc("DELETE /api/admin/item", s.handleAdminDeleteItem)
	return s.adminGuard(mux)
}

// adminGuard 是管理接口的访问闸门。
func (s *Server) adminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Admin.On() {
			http.Error(w, "管理接口已关闭（配置 admin.enabled=false）", http.StatusNotFound)
			return
		}
		if tok := strings.TrimSpace(s.cfg.Admin.Token); tok != "" {
			if r.Header.Get("X-Admin-Token") != tok {
				http.Error(w, "缺少或错误的 X-Admin-Token", http.StatusUnauthorized)
				return
			}
		} else if !isLoopback(r.RemoteAddr) {
			http.Error(w, "未配置 admin.token，管理接口仅允许本机访问", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback 判断请求来源是不是本机。IPv4 的 127/8 与 IPv6 的 ::1 都算。
func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if ip := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func (s *Server) handleAdminItem(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	kind, bad := kindOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	var body struct {
		ID       string            `json:"id"`
		Name     *string           `json:"name"`
		Descr    *string           `json:"descr"`
		Category *string           `json:"category"`
		Info     map[string]string `json:"info"`
		Edited   *bool             `json:"edited"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "请求体不是合法 JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		http.Error(w, "缺少 id", http.StatusBadRequest)
		return
	}
	it, ok, err := s.db.Get(kind, lang, id)
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "记录不存在: "+lang+"/"+string(kind)+"/"+id, http.StatusNotFound)
		return
	}
	e := store.ItemEdit{
		Name:     it.Name,
		Descr:    it.Descr,
		Category: it.Category,
		Edited:   it.Edited,
	}
	if body.Name != nil {
		e.Name = strings.TrimSpace(*body.Name)
	}
	if body.Descr != nil {
		e.Descr = strings.TrimSpace(*body.Descr)
	}
	if body.Category != nil {
		e.Category = strings.TrimSpace(*body.Category)
	}
	if body.Info != nil {
		e.Info = body.Info
	}
	if body.Edited != nil {
		e.Edited = *body.Edited
	}
	if _, err := s.db.UpdateItem(kind, lang, id, e); err != nil {
		http.Error(w, "写入失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.attrCacheClear()
	out, ok, err := s.db.Get(kind, lang, id)
	if err != nil || !ok {
		http.Error(w, "回读失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.toView(out))
}

func (s *Server) handleAdminDeleteItem(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "缺少参数 id", http.StatusBadRequest)
		return
	}
	kind, bad := kindOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	ok, err := s.db.DeleteItem(kind, lang, id)
	if err != nil {
		http.Error(w, "删除失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "记录不存在: "+lang+"/"+string(kind)+"/"+id, http.StatusNotFound)
		return
	}
	s.attrCacheClear()
	writeJSON(w, http.StatusOK, map[string]any{"deleted": lang + "/" + string(kind) + "/" + id})
}

func (s *Server) handleAdminFiles(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	vals := r.URL.Query()
	rows, total, err := s.db.ListFiles(lang,
		strings.TrimSpace(vals.Get("q")), vals.Get("status"),
		atoi(vals.Get("limit"), 50), atoi(vals.Get("offset"), 0))
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lang": lang, "total": total, "files": rows,
		"provenance": s.provenanceReady(lang),
	})
}

func (s *Server) handleAdminRuns(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	runs, err := s.db.ListRuns(lang, atoi(r.URL.Query().Get("limit"), 30))
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lang": lang, "runs": runs})
}

// handleAdminSources 回答"这条记录是哪个 XML 文件贡献的"，管理页据此一键重解析。
// 必须带 kind：物品与怪物的 ID 空间整体重叠，不带域就会把 Mob.wz 的文件列为物品的来源。
func (s *Server) handleAdminSources(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	kind, bad := kindOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "缺少参数 id", http.StatusBadRequest)
		return
	}
	paths, err := s.db.SourcesOf(kind, lang, id)
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lang": lang, "kind": string(kind), "id": id, "paths": paths})
}

// diffReply 是比对结果：四类计数 + 明细 + 库内手工修改行数 + 溯源是否可用。
type diffReply struct {
	Lang         string              `json:"lang"`
	Wz           string              `json:"wz"`
	Sources      []string            `json:"sources"`
	TookMSec     int64               `json:"tookMs"`
	Edited       int                 `json:"editedRows"`
	EditedByKind map[string]int      `json:"editedByKind"`
	Provenance   bool                `json:"provenance"`
	Disk         int                 `json:"diskFiles"`
	Stored       int                 `json:"storedFiles"`
	New          int                 `json:"new"`
	Changed      int                 `json:"changed"`
	Gone         int                 `json:"gone"`
	Unchanged    int                 `json:"unchanged"`
	Rows         []store.FileDiffRow `json:"rows"`
	Truncated    bool                `json:"truncated"`
}

func (s *Server) handleAdminDiff(w http.ResponseWriter, r *http.Request) {
	lang, loc, bad := s.langAndLocale(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	disk, err := scan.Collect(loc.Wz, loc.Sources)
	if err != nil {
		http.Error(w, "读取导出目录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	start := time.Now()
	limit := atoi(r.URL.Query().Get("limit"), 300)
	tot, rows, err := s.db.DiffFiles(lang, disk, r.URL.Query().Get("state"), limit)
	if err != nil {
		http.Error(w, "比对失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	edited, editedByKind, err := s.editedByKinds(lang)
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, diffReply{
		Lang: lang, Wz: loc.Wz, Sources: loc.Sources,
		TookMSec: time.Since(start).Milliseconds(),
		Edited:   edited, EditedByKind: editedByKind, Provenance: s.provenanceReady(lang),
		Disk: tot.TotalDisk, Stored: tot.TotalStore,
		New: tot.New, Changed: tot.Changed, Gone: tot.Gone, Unchanged: tot.Unchanged,
		Rows: rows, Truncated: limit > 0 && (tot.New+tot.Changed+tot.Gone) > limit,
	})
}

// reloadRequest 是重载入参。三种目标给法按优先级取一：
// paths（显式清单，可用 "/" 结尾表示整目录）> selector（比对状态）> all。
type reloadRequest struct {
	Paths           []string `json:"paths"`
	Selector        string   `json:"selector"`
	All             bool     `json:"all"`
	Merge           bool     `json:"merge"` // true 时维持"只增不减"的旧语义
	OverwriteEdited bool     `json:"overwriteEdited"`
}

func (s *Server) handleAdminReload(w http.ResponseWriter, r *http.Request) {
	lang, loc, bad := s.langAndLocale(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	var req reloadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<22)).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	paths := req.Paths
	if len(paths) == 0 && !req.All && req.Selector != "" {
		disk, err := scan.Collect(loc.Wz, loc.Sources)
		if err != nil {
			http.Error(w, "读取导出目录失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_, rows, err := s.db.DiffFiles(lang, disk, "", 0)
		if err != nil {
			http.Error(w, "比对失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		paths = store.SelectedPaths(rows, req.Selector)
	}
	if len(paths) == 0 && !req.All {
		http.Error(w, "没有需要重载的文件：给出 paths、selector 或 all", http.StatusBadRequest)
		return
	}
	opts := scan.Options{
		Lang: lang, Root: loc.Wz, Sources: loc.Sources, Workers: s.cfg.Workers,
		Paths: paths, Force: true, Overwrite: !req.Merge, SkipEdited: !req.OverwriteEdited,
		Batch: 200, Quiet: true,
	}
	if req.All && len(paths) == 0 {
		opts.Paths = nil // 全库重扫，交给 scan 自己枚举
	}
	id, err := s.startJob(&adminJob{
		Lang: lang, Kind: "reload", Target: len(paths),
		Note: reloadNote(req, paths),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	go s.runReload(id, opts)
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": id, "target": len(paths)})
}

func reloadNote(req reloadRequest, paths []string) string {
	switch {
	case req.All && len(paths) == 0:
		return "全库重扫"
	case len(req.Paths) > 0:
		return fmt.Sprintf("指定 %d 个文件", len(paths))
	case req.Selector != "":
		return "按比对状态 " + req.Selector
	}
	return ""
}

func (s *Server) runReload(id int64, opts scan.Options) {
	s.withJob(id, func(j *adminJob) { j.State = "running" })
	// 全库重扫提交时不知道文件数，靠回调把进度和总数补回来。
	opts.OnProgress = func(done, total int) {
		s.withJob(id, func(j *adminJob) { j.Done, j.Total, j.Target = done, total, total })
	}
	res, err := scan.Run(opts, s.db)
	s.withJob(id, func(j *adminJob) {
		j.Done = res.Parsed + res.Failed
		if j.Total == 0 {
			j.Total, j.Target = len(opts.Paths), len(opts.Paths)
		}
		j.Missing = res.Missing
		j.Summary = &jobStat{
			Parsed: res.Parsed, Failed: res.Failed, Names: res.Names, Infos: res.Infos,
			Cleared: res.Cleared, Protected: res.Protected,
			DurationMSec: res.Duration.Milliseconds(),
		}
	})
	if err != nil {
		s.finishJob(id, "error", err.Error())
		log.Printf("管理页重载失败（%s）: %v", opts.Lang, err)
		return
	}
	s.attrCacheClear()
	s.finishJob(id, "done", "")
	log.Printf("管理页重载完成（%s）：解析 %d 失败 %d 清掉失效贡献 %d 跳过手工修改 %d",
		opts.Lang, res.Parsed, res.Failed, res.Cleared, res.Protected)
}

// handleAdminPrune 清掉"库里已登记但磁盘上不存在"的文件记录及其独占贡献。
func (s *Server) handleAdminPrune(w http.ResponseWriter, r *http.Request) {
	lang, loc, bad := s.langAndLocale(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	var req struct {
		OverwriteEdited bool `json:"overwriteEdited"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	disk, err := scan.Collect(loc.Wz, loc.Sources)
	if err != nil {
		http.Error(w, "读取导出目录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, rows, err := s.db.DiffFiles(lang, disk, "gone", 0)
	if err != nil {
		http.Error(w, "比对失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(rows) == 0 {
		http.Error(w, "没有需要清理的文件记录", http.StatusBadRequest)
		return
	}
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	id, err := s.startJob(&adminJob{Lang: lang, Kind: "prune", Target: len(paths), Total: len(paths)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	go s.runPrune(id, lang, paths, !req.OverwriteEdited)
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": id, "target": len(paths)})
}

func (s *Server) runPrune(id int64, lang string, paths []string, skipEdited bool) {
	s.withJob(id, func(j *adminJob) { j.State = "running" })
	var files, cleared int
	for len(paths) > 0 {
		n := 500
		if n > len(paths) {
			n = len(paths)
		}
		f, c, err := s.db.DeleteFiles(lang, paths[:n], skipEdited)
		if err != nil {
			s.finishJob(id, "error", err.Error())
			log.Printf("管理页清理失败（%s）: %v", lang, err)
			return
		}
		files, cleared = files+f, cleared+c
		paths = paths[n:]
		s.withJob(id, func(j *adminJob) { j.Done = files })
	}
	s.attrCacheClear()
	s.withJob(id, func(j *adminJob) {
		j.Summary = &jobStat{DeletedFiles: files, Cleared: cleared}
	})
	s.finishJob(id, "done", "")
	log.Printf("管理页清理完成（%s）：删除文件记录 %d 条，连带清除物品 %d 条", lang, files, cleared)
}

func (s *Server) handleAdminJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": s.job.State == "running", "job": s.job.snapshot()})
}

// startJob 起一个单飞任务；已有任务在跑时返回错误。
func (s *Server) startJob(j *adminJob) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.State == "running" {
		return 0, fmt.Errorf("已有管理任务在跑（%s / %s），请等它结束", s.job.Lang, s.job.Kind)
	}
	s.jobSeq++
	j.ID = s.jobSeq
	j.State = "queued"
	j.StartedAt = time.Now()
	s.job = j
	return j.ID, nil
}

func (s *Server) withJob(id int64, fn func(*adminJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.ID == id {
		fn(s.job)
	}
}

func (s *Server) finishJob(id int64, state, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil || s.job.ID != id {
		return
	}
	now := time.Now()
	s.job.State, s.job.Err, s.job.FinishedAt = state, errMsg, &now
}

// langAndLocale 同时校验语言标识并取出它的 locale（管理接口需要 wz 目录）。
func (s *Server) langAndLocale(r *http.Request) (string, config.Locale, string) {
	lang, bad := s.langOf(r)
	if bad != "" {
		return "", config.Locale{}, bad
	}
	loc, ok := s.cfg.Locale(lang)
	if !ok {
		return "", config.Locale{}, "未配置的语言: " + lang
	}
	if loc.Wz == "" {
		return "", config.Locale{}, lang + " 未配置 wz 目录"
	}
	return lang, loc, ""
}

// provenanceReady 判断该语言域是否已有文件级溯源（旧库需重扫一次才有）。
func (s *Server) provenanceReady(lang string) bool {
	ok, err := s.db.HasProvenance(lang)
	return err == nil && ok
}

// editedByKinds 汇总各实体域的手工修改行数。管理页只给一个总数不够：
// 手工行按域保护，物品域改了 3 行不代表怪物域也会被跳过。
func (s *Server) editedByKinds(lang string) (int, map[string]int, error) {
	total := 0
	out := make(map[string]int, len(store.Kinds))
	for _, k := range store.Kinds {
		n, err := s.db.EditedCount(k, lang)
		if err != nil {
			return 0, nil, err
		}
		out[string(k)] = n
		total += n
	}
	return total, out, nil
}

// handleAdmin 出管理页 HTML。
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	page, err := staticFS.ReadFile("static/admin.html")
	if err != nil {
		http.Error(w, "内置页面缺失: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(page)
}
