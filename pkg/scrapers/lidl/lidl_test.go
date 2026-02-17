package lidl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScraper_Scrape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Received request for: %s", r.URL.Path)

		response := `
<!DOCTYPE html>
<html>
<head>
    <script>
        var unified_datalayer_product = {"id":"10045033","name":"Vitasia Nori Lachs","price":4.99,"currency":"EUR","brand":"Vitasia"};
    </script>
</head>
<body>
    <div class="ods-price__stroke-price">6.99 €</div>
    <div>AKTION</div>
    <div>In der Filiale 16.02. - 18.02.</div>
</body>
</html>
`
		fmt.Fprintln(w, response)
	}))
	defer ts.Close()

	scraper := NewScraper()
	scraper.BaseURL = ts.URL + "/p/"

	scraper.Collector.AllowedDomains = nil

	product, err := scraper.Scrape("10045033")
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if product.Name != "Vitasia Nori Lachs" {
		t.Errorf("Expected name 'Vitasia Nori Lachs', got '%s'", product.Name)
	}

	if product.Price != 4.99 {
		t.Errorf("Expected price 4.99, got %f", product.Price)
	}

	if product.OldPrice != 6.99 {
		t.Errorf("Expected old price 6.99, got %f", product.OldPrice)
	}

	if product.DiscountLabel != "AKTION" {
		t.Errorf("Expected discount label 'AKTION', got '%s'", product.DiscountLabel)
	}

	if product.AvailabilityLabel != "Filiale 16.02. - 18.02." {
		t.Errorf("Expected availability label 'Filiale 16.02. - 18.02.', got '%s'", product.AvailabilityLabel)
	}
}
