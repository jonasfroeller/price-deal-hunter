package main

import (
	"encoding/json"
	"fmt"
	"hunter-base/pkg/api"
	"hunter-base/pkg/cache"
	"hunter-base/pkg/logger"
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/billa"
	"hunter-base/pkg/scrapers/hofer"
	"hunter-base/pkg/scrapers/lidl"
	"hunter-base/pkg/scrapers/spar"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	scalargo "github.com/bdpiprava/scalar-go"
)

var (
	scraperSemaphore = make(chan struct{}, 3)
	productCache     *cache.Cache
)

func main() {
	port := "9090"

	dbPath := os.Getenv("CACHE_DB_PATH")
	if dbPath == "" {
		dbPath = "./cache.db"
	}

	ttlMinutes := 1440
	if val := os.Getenv("CACHE_TTL_MINUTES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			ttlMinutes = parsed
		}
	}

	var err error
	productCache, err = cache.New(dbPath, time.Duration(ttlMinutes)*time.Minute)
	if err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}
	defer productCache.Close()

	log.Printf("Cache initialized at %s with TTL %d minutes", dbPath, ttlMinutes)

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
		Handler:           nil,
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
	// Path expected: /stores/{store}/products/{id}
	parts := strings.Split(r.URL.Path, "/")
	// parts[0] = ""
	// parts[1] = "stores"
	// parts[2] = {store}
	// parts[3] = "products"
	// parts[4] = {id} or "batch"

	if len(parts) < 5 || parts[3] != "products" {
		api.WriteBadRequest(w, "Invalid path. Expected /stores/{store}/products/{id} or /stores/{store}/products/batch", r.URL.Path)
		return
	}

	store := strings.ToLower(parts[2])
	rawID := parts[4]

	if rawID == "batch" {
		if r.Method != http.MethodPost {
			api.WriteBadRequest(w, "Method not allowed for batch endpoint. Use POST.", r.URL.Path)
			return
		}
		handleBatchProducts(w, r, store)
		return
	}

	if r.Method != http.MethodGet {
		api.WriteBadRequest(w, "Method not allowed. Use GET for single product.", r.URL.Path)
		return
	}

	if store != "spar" && store != "billa" && store != "lidl" && store != "hofer" {
		api.WriteBadRequest(w, "Store not supported. Available: spar, billa, lidl, hofer", r.URL.Path)
		return
	}

	// Acquire semaphore to prevent system overload
	scraperSemaphore <- struct{}{}
	defer func() { <-scraperSemaphore }()

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

	product, err := getProduct(store, productID)

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

func scrapeProduct(store, productID string) (*models.Product, error) {
	switch store {
	case "spar":
		scraper := spar.NewScraper()
		return scraper.Scrape(productID)
	case "billa":
		scraper := billa.NewScraper()
		return scraper.Scrape(productID)
	case "lidl":
		scraper := lidl.NewScraper()
		return scraper.Scrape(productID)
	case "hofer":
		scraper := hofer.NewScraper()
		return scraper.Scrape(productID)
	default:
		return nil, fmt.Errorf("store not supported. Available: spar, billa, lidl, hofer")
	}
}

func getProduct(store, productID string) (*models.Product, error) {
	if cached, ok := productCache.Get(store, productID); ok {
		logger.Dedup("Cache hit for %s/%s", store, productID)
		return cached, nil
	}

	product, err := scrapeProduct(store, productID)
	if err != nil {
		return nil, err
	}

	productCache.Set(store, productID, product)
	return product, nil
}

func handleBatchProducts(w http.ResponseWriter, r *http.Request, store string) {
	if store != "spar" && store != "billa" && store != "lidl" && store != "hofer" {
		api.WriteBadRequest(w, "Store not supported. Available: spar, billa, lidl, hofer", r.URL.Path)
		return
	}

	var batch []map[string]any
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		api.WriteBadRequest(w, "Invalid JSON body. Expected array of objects.", r.URL.Path)
		return
	}
	defer r.Body.Close()

	for _, item := range batch {
		barcodeVal, ok := item["barcode"]
		if !ok {
			item["store_info"] = map[string]string{"error": "missing barcode block"}
			continue
		}

		var rawID string
		switch v := barcodeVal.(type) {
		case string:
			rawID = v
		case float64:
			rawID = fmt.Sprintf("%.0f", v)
		default:
			item["store_info"] = map[string]string{"error": "invalid barcode format"}
			continue
		}

		productID := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, rawID)

		if productID == "" {
			item["store_info"] = map[string]string{"error": "barcode must contain at least one digit"}
			continue
		}

		scraperSemaphore <- struct{}{}
		product, err := getProduct(store, productID)
		<-scraperSemaphore

		if err != nil {
			if err == models.ErrProductNotFound || strings.Contains(err.Error(), "product not found") {
				item["store_info"] = map[string]string{"error": "Product not found"}
			} else if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") || strings.Contains(err.Error(), "timeout") {
				item["store_info"] = map[string]string{"error": "Gateway Timeout"}
			} else {
				item["store_info"] = map[string]string{"error": err.Error()}
			}
		} else {
			item["store_info"] = product
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(batch); err != nil {
		log.Printf("Error encoding batch response: %v", err)
		api.WriteInternalServerError(w, fmt.Errorf("failed to encode response"), r.URL.Path)
	}
}
