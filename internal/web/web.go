// Package web 提供只读的物品检索接口和一个内置极简查询页。
//
// 刻意用标准库 net/http + go:embed，不引入 gin/前端构建链：
// 这一层只做 JSON 查询与一张静态页，单二进制交付时成本最低。
//
// 所有检索都限定在某个语言域（?lang=），语言清单来自 wzconfig 的 locales；
// 图标是可选的：没配 icons.enabled 时 ix 为 nil，接口不返回 icon 字段值，也不注册 /img。
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/config"
	"github.com/sqx6781268/MapleWzMeta/internal/icons"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
)

//go:embed static/*
var staticFS embed.FS

var attrKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)

// Server 是查询服务。
type Server struct {
	cfg   config.Config
	db    *store.DB
	icons *icons.Index

	// mu 保护 attrCache：属性键统计要展开 56k 行的 JSON，约 0.7s，按语言缓存一次即可。
	mu        sync.Mutex
	attrCache map[string][]metaAttr
}

// New 构造服务；ix 可为 nil。
func New(db *store.DB, cfg config.Config, ix *icons.Index) *Server {
	return &Server{cfg: cfg, db: db, icons: ix, attrCache: map[string][]metaAttr{}}
}

// Handler 注册全部路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/items", s.handleItems)
	mux.HandleFunc("GET /api/item", s.handleItem)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/categories", s.handleCategories)
	mux.HandleFunc("GET /api/langs", s.handleLangs)
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if s.icons != nil {
		mux.HandleFunc("GET /img/", s.icons.Serve)
	}
	return logRequests(mux)
}

// Serve 在 addr 上监听，直到进程退出。
func (s *Server) Serve(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("查询服务已启动：http://%s （语言：%s，图标：%s）",
		addr, strings.Join(s.cfg.Langs(), "/"), map[bool]string{true: "开", false: "关"}[s.icons != nil])
	return srv.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "内置页面缺失: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

// itemView 是对外输出的物品结构。
type itemView struct {
	Lang      string            `json:"lang"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Desc      string            `json:"descr"`
	Category  string            `json:"category"`
	Info      map[string]string `json:"info"`
	Icon      string            `json:"icon,omitempty"`
	HasName   bool              `json:"hasName"`
	HasInfo   bool              `json:"hasInfo"`
	UpdatedAt string            `json:"updatedAt"`
}

func (s *Server) toView(it store.ItemRow) itemView {
	v := itemView{
		Lang: it.Lang, ID: it.ID, Name: it.Name, Desc: it.Descr, Category: it.Category,
		Info: it.Info, HasName: it.HasName, HasInfo: it.HasInfo,
		UpdatedAt: it.Updated.Format("2006-01-02 15:04:05"),
	}
	if s.icons != nil && s.icons.Has(it.ID) {
		v.Icon = "/img/" + it.ID + ".png"
	}
	return v
}

func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	q, bad := queryFrom(r, lang)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	rows, total, err := s.db.Search(q)
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]itemView, 0, len(rows))
	for _, it := range rows {
		out = append(out, s.toView(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lang": lang, "total": total, "limit": q.Limit, "offset": q.Offset, "items": out,
	})
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
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
	it, ok, err := s.db.Get(lang, id)
	if err != nil {
		http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "物品不存在: "+lang+"/"+id, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.toView(it))
}

// statsView 在库内概况上追加与图标开关有关的服务端事实。
type statsView struct {
	store.Totals
	IconsOn bool `json:"iconsOn"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	st, err := s.db.Stats(lang)
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, statsView{Totals: st, IconsOn: s.icons != nil})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	cats, err := s.db.Categories(lang)
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

// langOption 是页面语言下拉的数据源：配置里的语言 + 库内实际规模。
type langOption struct {
	Lang  string `json:"lang"`
	Label string `json:"label"`
	Items int    `json:"items"`
	Named int    `json:"named"`
}

// metaAttr 是侧边栏属性条件下拉的一项，Count 来自库内真实统计。
type metaAttr struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Preset bool   `json:"preset"`
	Count  int    `json:"count"`
}

func (s *Server) handleLangs(w http.ResponseWriter, r *http.Request) {
	inDB := map[string]store.LangCount{}
	list, err := s.db.Langs()
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, l := range list {
		inDB[l.Lang] = l
	}
	out := make([]langOption, 0, len(s.cfg.Locales))
	for _, l := range s.cfg.Locales {
		c := inDB[l.Lang]
		out = append(out, langOption{Lang: l.Lang, Label: l.Label, Items: c.Items, Named: c.Named})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default": s.cfg.DefaultLang(), "langs": out,
	})
}

// catOption 是分类下拉的一项：英文目录名 + 中文说明 + 库内数量。
type catOption struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// metaReply 是 /api/meta 的返回体。
type metaReply struct {
	Lang          string      `json:"lang"`
	Uncategorized string      `json:"uncategorized"`
	Categories    []catOption `json:"categories"`
	Attrs         []metaAttr  `json:"attrs"`
}

// metaAttrs 按语言缓存"库内真实存在的属性键 + 中文说明"。
func (s *Server) metaAttrs(lang string) ([]metaAttr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.attrCache[lang]; ok {
		return v, nil
	}
	ks, err := s.db.AttrKeys(lang, 0)
	if err != nil {
		return nil, err
	}
	out := make([]metaAttr, 0, len(ks))
	for _, k := range ks {
		if isNoiseAttr(k.Key) {
			continue
		}
		m := metaAttr{Key: k.Key, Kind: "text", Count: k.Count}
		if d, ok := attrMetaOf[k.Key]; ok {
			m.Label, m.Kind, m.Preset = d.Label, d.Kind, d.Preset
		}
		out = append(out, m)
	}
	s.attrCache[lang] = out
	return out, nil
}

// handleMeta 一次给齐前端要用的字典：分类中文说明 + 属性键清单（含中文说明与常用标记）。
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	lang, bad := s.langOf(r)
	if bad != "" {
		http.Error(w, bad, http.StatusBadRequest)
		return
	}
	cats, err := s.db.Categories(lang)
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	attrs, err := s.metaAttrs(lang)
	if err != nil {
		http.Error(w, "统计失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := metaReply{Lang: lang, Uncategorized: store.NoCategoryName,
		Categories: make([]catOption, 0, len(cats)), Attrs: attrs}
	for _, c := range cats {
		label := categoryLabel(c.Name)
		if c.Name == store.NoCategoryName {
			label = "未分类"
		}
		out.Categories = append(out.Categories, catOption{Name: c.Name, Label: label, Count: c.Count})
	}
	writeJSON(w, http.StatusOK, out)
}

// langOf 校验 ?lang=，未给时用配置里的第一项。
func (s *Server) langOf(r *http.Request) (string, string) {
	v := strings.TrimSpace(r.URL.Query().Get("lang"))
	if v == "" {
		return s.cfg.DefaultLang(), ""
	}
	if _, ok := s.cfg.Locale(v); !ok {
		return "", "未配置的语言: " + v
	}
	return v, ""
}

// queryFrom 把 URL 参数换成检索条件；返回非空字符串表示参数非法。
func queryFrom(r *http.Request, lang string) (store.Query, string) {
	vals := r.URL.Query()
	q := store.Query{
		Lang:  lang,
		Limit: atoi(vals.Get("limit"), 50), Offset: atoi(vals.Get("offset"), 0),
	}
	if q.Limit < 1 {
		q.Limit = 1
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	q.OrderBy = vals.Get("order")
	q.Desc = vals.Get("desc") == "1"

	if v := strings.TrimSpace(vals.Get("id")); v != "" {
		q.ID = v
	}
	if v := strings.TrimSpace(vals.Get("cat")); v != "" {
		q.Category = v
	}
	// 关键字：纯数字按 ID 前缀查，否则按名称模糊查。
	if v := strings.TrimSpace(vals.Get("q")); v != "" {
		if isDigits(v) {
			q.IDPrefix = v
		} else {
			q.Name = v
		}
	}
	// 属性条件一律支持重复传：attr 等于、like 包含、min ≥、max ≤、has 只看键存在。
	for _, a := range vals["attr"] {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return q, "attr 参数需写成 key=value"
		}
		if !attrKeyRe.MatchString(k) {
			return q, "attr 键名不合法: " + k
		}
		q.Wants = append(q.Wants, k+"="+v)
	}
	for _, a := range vals["like"] {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return q, "like 参数需写成 key=子串"
		}
		if !attrKeyRe.MatchString(k) {
			return q, "like 键名不合法: " + k
		}
		q.Like = append(q.Like, k+"="+v)
	}
	if err := fillRange(vals["min"], "min", &q.GTE); err != nil {
		return q, err.Error()
	}
	if err := fillRange(vals["max"], "max", &q.LTE); err != nil {
		return q, err.Error()
	}
	for _, k := range vals["has"] {
		if !attrKeyRe.MatchString(k) {
			return q, "has 键名不合法: " + k
		}
		q.Has = append(q.Has, k)
	}
	switch vals.Get("complete") {
	case "all":
		t := true
		q.HasName, q.HasInfo = &t, &t
	case "named":
		t, f := true, false
		q.HasName, q.HasInfo = &t, &f
	case "info":
		f, t := false, true
		q.HasName, q.HasInfo = &f, &t
	}
	return q, ""
}

// fillRange 把 "k=整数" 形式的重复参数收进 map；缺键时自行创建。
func fillRange(list []string, name string, dst *map[string]int64) error {
	if len(list) == 0 {
		return nil
	}
	m := *dst
	if m == nil {
		m = map[string]int64{}
	}
	for _, v := range list {
		k, n, ok := strings.Cut(v, "=")
		if !ok || !attrKeyRe.MatchString(k) {
			return fmt.Errorf("%s 参数需写成 key=整数", name)
		}
		num, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return fmt.Errorf("%s 参数需写成 key=整数", name)
		}
		m[k] = num
	}
	*dst = m
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Printf("JSON 编码失败: %v", err)
	}
}

// statusWriter 用来记状态码。
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), sw.code, time.Since(start).Truncate(time.Millisecond))
	})
}

func atoi(s string, def int) int {
	if strings.TrimSpace(s) == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
