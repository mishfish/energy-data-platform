package main

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	periodLayout = "200601021504"
	// The real API rejects requests spanning more than a year.
	maxWindow = 366 * 24 * time.Hour
)

// server answers the subset of the Transparency Platform API that ingest uses.
type server struct {
	model  model
	faults faults
	now    func() time.Time
	logger *slog.Logger
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api", s.handleAPI)
	mux.Handle("/admin/fault", &s.faults)
	// Stateless: ready as soon as it listens.
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readyz", ok)
	return mux
}

func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if s.faults.apply(r.Context(), w) {
		s.logger.Info("fault injected", "query", redactedQuery(r.URL.Query()))
		return
	}
	q := r.URL.Query()
	if q.Get("securityToken") == "" {
		http.Error(w, "missing securityToken", http.StatusUnauthorized)
		return
	}
	start, end, err := parseWindow(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := s.now()

	var (
		doc   any
		found bool
		item  string
		zone  string
	)
	switch docType := q.Get("documentType"); docType {
	case "A44":
		zone = q.Get("in_Domain")
		if zone == "" || zone != q.Get("out_Domain") {
			http.Error(w, "A44 needs in_Domain and out_Domain set to the same bidding zone", http.StatusBadRequest)
			return
		}
		item = "Day-ahead Prices [12.1.D]"
		doc, found = s.model.priceDocument(zone, start, end, now)
	case "A65":
		zone = q.Get("outBiddingZone_Domain")
		if zone == "" || q.Get("processType") != "A16" {
			http.Error(w, "A65 is served for processType A16 with outBiddingZone_Domain only", http.StatusBadRequest)
			return
		}
		item = "Actual Total Load [6.1.A]"
		doc, found = s.model.loadDocument(zone, start, end, now)
	default:
		http.Error(w, fmt.Sprintf("documentType %q is not served by the fake API", docType), http.StatusBadRequest)
		return
	}
	if !found {
		doc = noDataAcknowledgement(item, zone, start, end, now)
	}
	s.logger.Info("document served", "query", redactedQuery(q), "no_data", !found)
	writeXML(w, doc)
}

// parseWindow reads periodStart and periodEnd (UTC, yyyyMMddHHmm).
func parseWindow(q url.Values) (start, end time.Time, err error) {
	start, err = time.Parse(periodLayout, q.Get("periodStart"))
	if err != nil {
		return start, end, fmt.Errorf("invalid periodStart %q", q.Get("periodStart"))
	}
	end, err = time.Parse(periodLayout, q.Get("periodEnd"))
	if err != nil {
		return start, end, fmt.Errorf("invalid periodEnd %q", q.Get("periodEnd"))
	}
	if !end.After(start) || end.Sub(start) > maxWindow {
		return start, end, fmt.Errorf("window %s/%s is empty or longer than a year", start, end)
	}
	return start, end, nil
}

// writeXML answers 200 with doc. The real API sends "no data" acknowledgements with 200 as well.
func writeXML(w http.ResponseWriter, doc any) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		http.Error(w, "marshal document: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

// redactedQuery returns the query for logging, without the token.
func redactedQuery(q url.Values) string {
	c := url.Values{}
	for k, v := range q {
		if k != "securityToken" {
			c[k] = v
		}
	}
	return c.Encode()
}
