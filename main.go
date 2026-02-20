package main

import (
	"encoding/json"
	"fmt"
	"hunter-base/pkg/api"
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/billa"
	"hunter-base/pkg/scrapers/hofer"
	"hunter-base/pkg/scrapers/lidl"
	"hunter-base/pkg/scrapers/spar"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	scalargo "github.com/bdpiprava/scalar-go"
)

var scraperSemaphore = make(chan struct{}, 3)

func main() {
	port := "9090"

	// Root handler - serves Scalar docs on /, API on /stores/
	http.HandleFunc("/", rootHandler)

	ip := GetOutboundIP()
	if ip != nil {
		fmt.Printf("Local Network URL: http://%s:%s\n", ip.String(), port)
	} else {
		fmt.Println("Could not determine local IP address.")
	}
	fmt.Printf("Access URL: http://localhost:%s\n", port)
	fmt.Printf("API Docs: http://localhost:%s/\n", port)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           nil, // Uses DefaultServeMux
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	// API requests go to product handler
	if strings.HasPrefix(r.URL.Path, "/stores/") {
		productHandler(w, r)
		return
	}

	// Serve Scalar docs on root path
	html, err := scalargo.NewV2(
		scalargo.WithSpecDir("./"),
		scalargo.WithMetaDataOpts(
			scalargo.WithTitle("Price Deal Hunter API"),
		),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		addrs, _ := net.InterfaceAddrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP
				}
			}
		}
		return nil
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func productHandler(w http.ResponseWriter, r *http.Request) {
	// Acquire semaphore to prevent system overload
	scraperSemaphore <- struct{}{}
	defer func() { <-scraperSemaphore }()

	// Path expected: /stores/{store}/products/{id}
	parts := strings.Split(r.URL.Path, "/")
	// parts[0] = ""
	// parts[1] = "stores"
	// parts[2] = {store}
	// parts[3] = "products"
	// parts[4] = {id}

	if len(parts) < 5 || parts[3] != "products" {
		api.WriteBadRequest(w, "Invalid path. Expected /stores/{store}/products/{id}", r.URL.Path)
		return
	}

	store := strings.ToLower(parts[2])
	rawID := parts[4]

	// Filter out non-numeric characters from the ID
	// e.g. "00-626061" -> "00626061"
	productID := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, rawID)

	if productID == "" {
		api.WriteBadRequest(w, fmt.Sprintf("Invalid product ID: %s. Must contain at least one digit.", rawID), r.URL.Path)
		return
	}

	var product *models.Product
	var err error

	switch store {
	case "spar":
		scraper := spar.NewScraper()
		product, err = scraper.Scrape(productID)
	case "billa":
		scraper := billa.NewScraper()
		product, err = scraper.Scrape(productID)
	case "lidl":
		scraper := lidl.NewScraper()
		product, err = scraper.Scrape(productID)
	case "hofer":
		scraper := hofer.NewScraper()
		product, err = scraper.Scrape(productID)
	default:
		api.WriteBadRequest(w, "Store not supported. Available: spar, billa, lidl, hofer", r.URL.Path)
		return
	}

	if err != nil {
		log.Printf("Error scraping %s %s: %v", store, productID, err)

		if err == models.ErrProductNotFound || strings.Contains(err.Error(), "product not found") {
			api.WriteNotFound(w, "Product not found", r.URL.Path)
			return
		}

		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") || strings.Contains(err.Error(), "timeout") {
			api.WriteError(w, http.StatusGatewayTimeout, "Gateway Timeout", "Upstream service timed out: "+err.Error(), r.URL.Path)
			return
		}

		api.WriteInternalServerError(w, err, r.URL.Path)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(product); err != nil {
		log.Printf("Error encoding response: %v", err)
		api.WriteInternalServerError(w, fmt.Errorf("failed to encode response"), r.URL.Path)
	}
}
