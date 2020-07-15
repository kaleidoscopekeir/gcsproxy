package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"google.golang.org/api/option"
)

var (
	bind        = flag.String("b", "127.0.0.1:8080", "Bind address")
	verbose     = flag.Bool("v", false, "Show access log")
	credentials = flag.String("c", "", "The path to the keyfile. If not present, client will use your default application credentials.")
)

var (
	client *storage.Client
	ctx    = context.Background()
)

func handleError(w http.ResponseWriter, err error) {
	if err != nil {
		if err == storage.ErrObjectNotExist {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
}

func header(r *http.Request, key string) (string, bool) {
	if r.Header == nil {
		return "", false
	}
	if candidate := r.Header[key]; len(candidate) > 0 {
		return candidate[0], true
	}
	return "", false
}

func setStrHeader(w http.ResponseWriter, key string, value string) {
	if value != "" {
		w.Header().Add(key, value)
	}
}

func setIntHeader(w http.ResponseWriter, key string, value int64) {
	if value > 0 {
		w.Header().Add(key, strconv.FormatInt(value, 10))
	}
}

func setTimeHeader(w http.ResponseWriter, key string, value time.Time) {
	if !value.IsZero() {
		w.Header().Add(key, value.UTC().Format(http.TimeFormat))
	}
}

type wrapResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *wrapResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.status = status
}

func wrapper(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proc := time.Now()
		writer := &wrapResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		fn(writer, r)
		addr := r.RemoteAddr
		if ip, found := header(r, "X-Forwarded-For"); found {
			addr = ip
		}
		if *verbose {
			log.Printf("[%s] %.3f %d %s %s",
				addr,
				time.Now().Sub(proc).Seconds(),
				writer.status,
				r.Method,
				r.URL,
			)
		}
	}
}

func proxy(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	obj := client.Bucket(params["bucket"]).Object(params["object"])
	switch r.Method {
		case http.MethodGet, http.MethodHead:
			attr, err := obj.Attrs(ctx)
			if err != nil {
				handleError(w, err)
				return
			}
			objr, err := obj.NewReader(ctx)
			if err != nil {
				handleError(w, err)
				return
			}
			u, err := url.Parse(r.RequestURI)
			if err != nil {
				handleError(w, err)
				return
			}
			if u.Query().Get("alt") != "media" {
				setStrHeader(w, "Content-Type", "application/json")
				jsonMetadata, err := json.Marshal(attr)
				if err != nil {
					handleError(w, err)
					return
				}
				w.Write(jsonMetadata)
			} else {
				setStrHeader(w, "Content-Type", attr.ContentType)
				setStrHeader(w, "Content-Language", attr.ContentLanguage)
				setStrHeader(w, "Cache-Control", attr.CacheControl)
				setStrHeader(w, "Content-Encoding", attr.ContentEncoding)
				setStrHeader(w, "Content-Disposition", attr.ContentDisposition)
				setIntHeader(w, "Content-Length", attr.Size)
				io.Copy(w, objr)
			}
		case http.MethodPost, http.MethodPut:
			wc := obj.NewWriter(ctx)
			fileData, err := ioutil.ReadAll(r.Body)
			if err != nil {
				handleError(w, err)
				return
			}
			if _, err := wc.Write(fileData); err != nil {
				handleError(w, err)
				return
			}
			if err := wc.Close(); err != nil {
				handleError(w, err)
				return
			}
		case http.MethodDelete:
			if err := obj.Delete(ctx); err != nil {
				handleError(w, err)
				return
			}
		default:
			http.Error(w, "Method Not Allowed", http.StatusNotFound)
			return
	}
}

func main() {
	flag.Parse()

	var err error
	if *credentials != "" {
		client, err = storage.NewClient(ctx, option.WithCredentialsFile(*credentials))
	} else {
		client, err = storage.NewClient(ctx)
	}
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	r := mux.NewRouter()
	r.HandleFunc("/{bucket:[0-9a-zA-Z-_.]+}/{object:.*}", wrapper(proxy)).Methods("GET", "HEAD", "PUT", "POST", "DELETE")

	log.Printf("[service] listening on %s", *bind)
	if err := http.ListenAndServe(*bind, r); err != nil {
		log.Fatal(err)
	}
}
