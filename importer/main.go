package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/index.html
var indexTpl string

var (
	listenAddr  = getenv("LISTEN_ADDR", ":3161")
	ollamaURL   = getenv("OLLAMA_URL", "http://ollama:11434")
	uploadDir   = getenv("UPLOAD_DIR", "/uploads")
	modelsInOll = getenv("MODELS_IN_OLLAMA", "/models/imports")
	maxUpload   = int64(50) << 30 // 50 GiB
)

func main() {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", uploadDir, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(handleIndex))
	mux.HandleFunc("/upload", handleUpload)
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

type pageData struct {
	DefaultName string
	Error       string
	OK          string
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	tpl := template.Must(template.New("index").Parse(indexTpl))
	_ = tpl.Execute(w, pageData{
		DefaultName: r.URL.Query().Get("name"),
		Error:       r.URL.Query().Get("error"),
		OK:          r.URL.Query().Get("ok"),
	})
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)

	if err := r.ParseMultipartForm(maxUpload + 1<<20); err != nil {
		redirect(w, r, "/?error="+url.QueryEscape(err.Error()))
		return
	}

	modelfileRaw, err := readModelfile(r)
	if err != nil {
		redirect(w, r, "/?error="+url.QueryEscape(err.Error()))
		return
	}

	fromPath, fromErr := extractFromPath(modelfileRaw)
	if fromErr != nil {
		redirect(w, r, "/?error="+url.QueryEscape(fromErr.Error()))
		return
	}

	uploadedName, found := findUploadedFile(r, filepath.Base(fromPath))
	if !found {
		redirect(w, r, "/?error="+url.QueryEscape(
			"uploaded files do not include "+filepath.Base(fromPath)+
				" (referenced in the Modelfile's FROM line)"))
		return
	}

	stagedDir := filepath.Join(modelsInOll, sanitizeName(strings.TrimSuffix(uploadedName, filepath.Ext(uploadedName))))
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		redirect(w, r, "/?error="+url.QueryEscape(err.Error()))
		return
	}
	dst := filepath.Join(stagedDir, uploadedName)
	if err := moveUploadedFile(filepath.Join(uploadDir, uploadedName), dst); err != nil {
		redirect(w, r, "/?error="+url.QueryEscape(err.Error()))
		return
	}

	rewritten := rewriteFrom(modelfileRaw, dst)

	modelName := strings.TrimSpace(r.FormValue("name"))
	if modelName == "" {
		modelName = deriveModelName(modelfileRaw, uploadedName)
	}
	if modelName == "" {
		redirect(w, r, "/?error="+url.QueryEscape("could not derive model name; add a '# model: name:tag' line to the Modelfile or set the name field"))
		return
	}

	if err := ollamaCreate(modelName, rewritten); err != nil {
		redirect(w, r, "/?error=ollama+create+failed%3A+"+url.QueryEscape(err.Error()))
		return
	}

	redirect(w, r, "/?ok="+url.QueryEscape("imported "+modelName))
}

func readModelfile(r *http.Request) (string, error) {
	if f, _, err := r.FormFile("modelfile"); err == nil {
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b), nil
	}
	if v := strings.TrimSpace(r.FormValue("modelfile_text")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing Modelfile (upload a file or paste contents)")
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

func findUploadedFile(r *http.Request, want string) (string, bool) {
	for _, fh := range r.MultipartForm.File["files"] {
		safe := filepath.Base(fh.Filename)
		if safe != want {
			continue
		}
		if err := saveUpload(fh, filepath.Join(uploadDir, safe)); err != nil {
			return "", false
		}
		return safe, true
	}
	return "", false
}

func saveUpload(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

func moveUploadedFile(src, dst string) error {
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
	_ = os.Remove(src)
	return nil
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
	base := strings.TrimSuffix(fallback, filepath.Ext(fallback))
	if i := strings.Index(base, "-int4-ov"); i > 0 {
		base = base[:i]
	}
	return base + ":latest"
}

func ollamaCreate(name, modelfile string) error {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("name", name); err != nil {
		return err
	}
	if err := mw.WriteField("modelfile", modelfile); err != nil {
		return err
	}
	if err := mw.WriteField("stream", "false"); err != nil {
		return err
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(ollamaURL, "/")+"/api/create", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

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

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}
