package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const indexTpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Ollama OpenVINO · Model Importer</title>
  <style>
    :root { color-scheme: dark; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
      margin: 0; padding: 2rem; max-width: 760px; margin-inline: auto;
      background: #0f1115; color: #e6e8eb;
    }
    h1 { margin-top: 0; font-size: 1.6rem; }
    .card { background: #1a1d24; border: 1px solid #2a2f3a; border-radius: 12px; padding: 1.5rem; margin: 1rem 0; }
    label { display: block; margin: 0.75rem 0 0.25rem; font-weight: 600; }
    input[type=text], input[type=file], textarea {
      width: 100%; box-sizing: border-box; padding: 0.6rem 0.75rem; border-radius: 8px;
      border: 1px solid #2a2f3a; background: #0f1115; color: #e6e8eb; font-family: inherit; font-size: 0.95rem;
    }
    textarea { min-height: 180px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85rem; }
    button {
      margin-top: 1.25rem; padding: 0.75rem 1rem; font-size: 1rem; font-weight: 600;
      background: #4f46e5; color: white; border: 0; border-radius: 8px; cursor: pointer;
    }
    button:hover { background: #4338ca; }
    button:disabled { background: #3a3f4b; cursor: not-allowed; }
    .alert { padding: 0.75rem 1rem; border-radius: 8px; margin: 1rem 0; }
    .alert.ok    { background: #064e3b; color: #d1fae5; }
    .alert.err   { background: #7f1d1d; color: #fee2e2; }
    code { background: #0b0d12; padding: 0.1rem 0.35rem; border-radius: 4px; font-size: 0.85em; }
    small { color: #9aa3b2; }
    .progress { width: 100%; height: 18px; background: #0b0d12; border: 1px solid #2a2f3a; border-radius: 6px; overflow: hidden; margin-top: .5rem; }
    .progress > div { height: 100%; background: #4f46e5; width: 0%; transition: width .2s; }
    .status { margin-top: .5rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85rem; }
  </style>
</head>
<body>
  <h1>OpenVINO Model Importer</h1>
  <p><small>Imports an OpenVINO-IR tar.gz into Ollama via <code>ollama create</code>. Large files are uploaded in chunks so they survive slow links and timeouts.</small></p>

  <form class="card" id="imp">
    <label>Model name (optional)</label>
    <input type="text" id="name" placeholder="DeepSeek-R1-Distill-Qwen-7B-int4-ov:v1">
    <small>If empty, the importer uses the first <code># model: name:tag</code> line of the Modelfile, or the tar.gz filename.</small>

    <label>Model file (the tar.gz referenced by FROM)</label>
    <input type="file" id="file" required>

    <label>Modelfile</label>
    <textarea id="modelfile" placeholder="FROM DeepSeek-R1-Distill-Qwen-7B-int4-ov.tar.gz
ModelType &quot;OpenVINO&quot;
InferDevice &quot;GPU&quot;
PARAMETER repeat_penalty 1.0
PARAMETER top_p 1.0
PARAMETER temperature 1.0"></textarea>

    <button type="submit" id="go">Import model</button>
    <div class="progress" id="barWrap" style="display:none"><div id="bar"></div></div>
    <div class="status" id="status"></div>
  </form>

  <div class="card">
    <strong>Notes</strong>
    <ul>
      <li>Upload size: 32 MiB chunks, retried automatically on failure.</li>
      <li>You can close this tab during the upload; reopen it and the import will continue when the importer is reachable again.</li>
      <li>The Modelfile's <code>FROM</code> line is auto-rewritten to a path the Ollama container can read.</li>
    </ul>
  </div>

<script>
const CHUNK = 32 * 1024 * 1024;

const $ = (s) => document.querySelector(s);
const form = $("#imp");
const fileInput = $("#file");
const nameInput = $("#name");
const modelfileInput = $("#modelfile");
const goBtn = $("#go");
const bar = $("#bar");
const barWrap = $("#barWrap");
const statusEl = $("#status");

function setStatus(html, cls) {
  statusEl.innerHTML = html;
  statusEl.className = "status " + (cls || "");
}

function fmtBytes(n) {
  const u = ["B","KiB","MiB","GiB","TiB"];
  let i = 0;
  while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
  return n.toFixed(n >= 10 ? 0 : 1) + " " + u[i];
}

async function postJSON(url, body) {
  const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const t = await r.text();
  if (!r.ok) throw new Error("HTTP " + r.status + ": " + t);
  return JSON.parse(t);
}

async function putBytes(url, blob) {
  const r = await fetch(url, { method: "PUT", headers: { "Content-Type": "application/octet-stream" }, body: blob });
  if (!r.ok) throw new Error("PUT " + r.status + ": " + await r.text());
}

async function pollJob(id) {
  const start = Date.now();
  while (true) {
    const r = await fetch("/jobs/" + id);
    if (!r.ok) throw new Error("status " + r.status);
    const j = await r.json();
    if (j.status === "done")   { return j; }
    if (j.status === "error")  { throw new Error(j.message || "import failed"); }
    if (Date.now() - start > 30 * 60 * 1000) throw new Error("timeout waiting for ollama create");
    await new Promise(r => setTimeout(r, 1000));
  }
}

async function uploadWithRetry(url, blob, attempts = 6) {
  for (let i = 0; i < attempts; i++) {
    try { await putBytes(url, blob); return; }
    catch (e) {
      if (i === attempts - 1) throw e;
      await new Promise(r => setTimeout(r, 500 * (i + 1)));
    }
  }
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  goBtn.disabled = true;
  barWrap.style.display = "block";
  bar.style.width = "0%";
  setStatus("Creating job…");

  try {
    const file = fileInput.files[0];
    if (!file) throw new Error("pick a file");
    const modelfile = modelfileInput.value.trim();
    if (!modelfile) throw new Error("paste a Modelfile");

    const job = await postJSON("/jobs", {
      name: nameInput.value.trim(),
      modelfile: modelfile,
      filename: file.name,
      size: file.size
    });

    setStatus("Uploading " + job.totalChunks + " chunks…");
    let sent = 0;
    for (let i = 0; i < job.totalChunks; i++) {
      const start = i * CHUNK;
      const end = Math.min(start + CHUNK, file.size);
      const blob = file.slice(start, end);
      await uploadWithRetry("/jobs/" + job.id + "/chunks/" + i, blob);
      sent += end - start;
      const pct = file.size > 0 ? (sent / file.size * 100) : 100;
      bar.style.width = pct.toFixed(1) + "%";
      setStatus("Uploading chunk " + (i + 1) + " / " + job.totalChunks + " · " + fmtBytes(sent) + " / " + fmtBytes(file.size));
    }

    setStatus("Finalizing and importing into Ollama…");
    const fr = await fetch("/jobs/" + job.id + "/finalize", { method: "POST" });
    if (!fr.ok && fr.status !== 202) throw new Error("finalize failed: HTTP " + fr.status);
    const done = await pollJob(job.id);
    setStatus('<div class="alert ok">Imported <code>' + done.name + '</code></div>', "ok");
  } catch (err) {
    setStatus('<div class="alert err">' + (err.message || err) + '</div>', "err");
  } finally {
    goBtn.disabled = false;
  }
});
</script>
</body>
</html>`

var (
	listenAddr  = getenv("LISTEN_ADDR", ":3161")
	ollamaURL   = getenv("OLLAMA_URL", "http://ollama:11434")
	uploadDir   = getenv("UPLOAD_DIR", "/uploads")
	modelsInOll = getenv("MODELS_IN_OLLAMA", "/models/imports")
	chunkSize   = int64(32) << 20 // 32 MiB
)

type jobStatus string

const (
	statusPending   jobStatus = "pending"
	statusUploading jobStatus = "uploading"
	statusCreating  jobStatus = "creating"
	statusDone      jobStatus = "done"
	statusError     jobStatus = "error"
)

type job struct {
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	Filename   string    `json:"filename"`
	Size       int64     `json:"size"`
	ChunkSize  int64     `json:"chunkSize"`
	TotalChunks int64    `json:"totalChunks"`
	Modelfile  string    `json:"-"`
	Status     jobStatus `json:"status"`
	Message    string    `json:"message,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*job{}
)

func main() {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", uploadDir, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/jobs", handleCreateJob)
	mux.HandleFunc("/jobs/", handleJobAction)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("importer listening on %s -> ollama %s", listenAddr, ollamaURL)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

type pageData struct{}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	tpl := template.Must(template.New("index").Parse(indexTpl))
	_ = tpl.Execute(w, pageData{})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- /jobs (POST) ---

type createJobReq struct {
	Name      string `json:"name"`
	Modelfile string `json:"modelfile"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
}

func handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req createJobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Filename == "" || req.Size <= 0 {
		http.Error(w, "filename and size required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Modelfile) == "" {
		http.Error(w, "modelfile required", http.StatusBadRequest)
		return
	}
	if _, err := extractFromPath(req.Modelfile); err != nil {
		http.Error(w, "modelfile: "+err.Error(), http.StatusBadRequest)
		return
	}

	j := &job{
		ID:          newID(),
		Name:        strings.TrimSpace(req.Name),
		Filename:    filepath.Base(req.Filename),
		Size:        req.Size,
		ChunkSize:   chunkSize,
		TotalChunks: (req.Size + chunkSize - 1) / chunkSize,
		Modelfile:   req.Modelfile,
		Status:      statusPending,
		CreatedAt:   time.Now(),
	}
	if err := os.MkdirAll(filepath.Join(uploadDir, j.ID), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jobsMu.Lock()
	jobs[j.ID] = j
	jobsMu.Unlock()

	writeJSON(w, http.StatusCreated, j)
}

// --- /jobs/{id}/... ---

func handleJobAction(w http.ResponseWriter, r *http.Request) {
	// Trim leading /jobs/ and split.
	rest := strings.TrimPrefix(r.URL.Path, "/jobs/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	verb := ""
	if len(parts) == 2 {
		verb = parts[1]
	}

	jobsMu.Lock()
	j, ok := jobs[id]
	jobsMu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch {
	case verb == "" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, j)
	case strings.HasPrefix(verb, "chunks/") && r.Method == http.MethodPut:
		nStr := strings.TrimPrefix(verb, "chunks/")
		n, err := strconv.ParseInt(nStr, 10, 64)
		if err != nil {
			http.Error(w, "bad chunk index", http.StatusBadRequest)
			return
		}
		handlePutChunk(w, r, j, n)
	case verb == "finalize" && r.Method == http.MethodPost:
		handleFinalize(w, r, j)
	default:
		http.NotFound(w, r)
	}
}

func handlePutChunk(w http.ResponseWriter, r *http.Request, j *job, n int64) {
	if n < 0 || n >= j.TotalChunks {
		http.Error(w, "chunk out of range", http.StatusBadRequest)
		return
	}
	expected := j.ChunkSize
	if n == j.TotalChunks-1 {
		expected = j.Size - n*j.ChunkSize
	}
	if r.ContentLength > 0 && r.ContentLength != expected {
		http.Error(w, fmt.Sprintf("chunk size %d != expected %d", r.ContentLength, expected), http.StatusBadRequest)
		return
	}

	path := filepath.Join(uploadDir, j.ID, fmt.Sprintf("part-%08d", n))
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n2, err := io.Copy(f, r.Body)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n2 != expected {
		_ = os.Remove(tmp)
		http.Error(w, fmt.Sprintf("short chunk: got %d expected %d", n2, expected), http.StatusBadRequest)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleFinalize(w http.ResponseWriter, r *http.Request, j *job) {
	jobsMu.Lock()
	if j.Status == statusDone || j.Status == statusCreating {
		jobsMu.Unlock()
		writeJSON(w, http.StatusOK, j)
		return
	}
	j.Status = statusCreating
	jobsMu.Unlock()

	go func() {
		if err := assembleAndImport(j); err != nil {
			jobsMu.Lock()
			j.Status = statusError
			j.Message = err.Error()
			jobsMu.Unlock()
			log.Printf("job %s: %v", j.ID, err)
			return
		}
		jobsMu.Lock()
		j.Status = statusDone
		j.Message = "imported " + j.Name
		jobsMu.Unlock()
		log.Printf("job %s: done -> %s", j.ID, j.Name)
	}()
	writeJSON(w, http.StatusAccepted, j)
}

func assembleAndImport(j *job) error {
	assembled := filepath.Join(uploadDir, j.ID, j.Filename)
	out, err := os.Create(assembled)
	if err != nil {
		return err
	}
	for n := int64(0); n < j.TotalChunks; n++ {
		part := filepath.Join(uploadDir, j.ID, fmt.Sprintf("part-%08d", n))
		in, err := os.Open(part)
		if err != nil {
			out.Close()
			return fmt.Errorf("missing chunk %d: %w", n, err)
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			out.Close()
			return err
		}
	}
	if err := out.Close(); err != nil {
		return err
	}

	// Stage into the shared volume visible to the ollama container.
	stagedDir := filepath.Join(modelsInOll, sanitizeName(stripExt(j.Filename)))
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		return err
	}
	staged := filepath.Join(stagedDir, j.Filename)
	if err := moveFile(assembled, staged); err != nil {
		return err
	}

	// Resolve model name
	name := j.Name
	if name == "" {
		name = deriveModelName(j.Modelfile, j.Filename)
	}
	if name == "" {
		return fmt.Errorf("could not derive model name; add '# model: name:tag' to the Modelfile or set the name field")
	}

	rewritten := rewriteFrom(j.Modelfile, staged)
	if err := ollamaCreate(name, rewritten); err != nil {
		return err
	}
	j.Name = name
	return nil
}

func extractFromPath(mf string) (string, error) {
	for _, line := range strings.Split(mf, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		upper := strings.ToUpper(trim)
		if !strings.HasPrefix(upper, "FROM ") {
			return "", fmt.Errorf("Modelfile must start with FROM <path>")
		}
		path := strings.TrimSpace(trim[5:])
		path = strings.Trim(path, "\"'")
		if path == "" {
			return "", fmt.Errorf("FROM path is empty")
		}
		return path, nil
	}
	return "", fmt.Errorf("Modelfile is empty")
}

func rewriteFrom(mf, newPath string) string {
	out := make([]string, 0, strings.Count(mf, "\n")+1)
	replaced := false
	for _, line := range strings.Split(mf, "\n") {
		trim := strings.TrimSpace(line)
		if !replaced && strings.HasPrefix(strings.ToUpper(trim), "FROM ") {
			out = append(out, fmt.Sprintf("FROM %s", newPath))
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		out = append([]string{fmt.Sprintf("FROM %s", newPath)}, out...)
	}
	return strings.Join(out, "\n")
}

func deriveModelName(mf, fallback string) string {
	for _, line := range strings.Split(mf, "\n") {
		trim := strings.TrimSpace(line)
		lower := strings.ToLower(trim)
		if strings.HasPrefix(lower, "# model:") {
			return strings.TrimSpace(trim[len("# model:"):])
		}
	}
	base := stripExt(fallback)
	if i := strings.Index(base, "-int4-ov"); i > 0 {
		base = base[:i]
	}
	return base + ":latest"
}

func ollamaCreate(name, modelfile string) error {
	stream := false
	reqBody := map[string]any{
		"model":     name,
		"modelfile": modelfile,
		"stream":    &stream,
	}
	for k, v := range parseOpenVINOFields(modelfile) {
		reqBody[k] = v
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(ollamaURL, "/")+"/api/create", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, string(respBody))
	}
	return nil
}

// parseOpenVINOFields lifts the OpenVINO-specific Modelfile directives into
// top-level JSON fields expected by the ollama_openvino fork's /api/create.
func parseOpenVINOFields(mf string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(mf, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		idx := strings.IndexAny(t, " \t")
		if idx < 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(t[:idx]))
		val := strings.TrimSpace(t[idx+1:])
		val = strings.Trim(val, "\"'")
		switch key {
		case "MODELBACKEND":
			out["modelbackend"] = val
		case "MODELTYPE":
			out["modeltype"] = val
		case "INFERDEVICE":
			out["inferdevice"] = val
		}
	}
	return out
}

func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func stripExt(s string) string {
	return strings.TrimSuffix(s, filepath.Ext(s))
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Remove(src)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
