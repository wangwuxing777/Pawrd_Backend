package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
)

func TestMaterializeClinicPhotoURLDoesNotExposeAPIKey(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://api.pawrd.top/clinics", nil)
	clinic := models.Clinic{
		GooglePlaceID:  "test-place",
		PhotoReference: "photo-reference",
	}

	materializeClinicPhotoURL(request, &clinic)

	if strings.Contains(clinic.PhotoURL, "key=") {
		t.Fatalf("photo URL exposed an API key: %s", clinic.PhotoURL)
	}
	if !strings.Contains(clinic.PhotoURL, "/api/maps/place-photo?") {
		t.Fatalf("unexpected proxy URL: %s", clinic.PhotoURL)
	}
	if !strings.Contains(clinic.PhotoURL, "place_id=test-place") {
		t.Fatalf("photo URL does not carry the Google place ID: %s", clinic.PhotoURL)
	}
}

func TestPlacePhotoProxyRejectsMissingReference(t *testing.T) {
	handler := NewPlacePhotoProxyHandler(&config.Config{MapsAPIKey: "test-key"})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/maps/place-photo", nil)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPlacePhotoProxyRejectsInvalidWidth(t *testing.T) {
	handler := NewPlacePhotoProxyHandler(&config.Config{MapsAPIKey: "test-key"})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/maps/place-photo?reference=ref&maxwidth=9999", nil)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPlacePhotoProxyUsesPlacesAPINew(t *testing.T) {
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "" {
			t.Fatalf("Places API key leaked to redirected image host")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg"))
	}))
	defer imageServer.Close()

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/places/test-place":
			if r.Header.Get("X-Goog-Api-Key") != "places-key" {
				t.Fatalf("missing Places API key header")
			}
			if r.Header.Get("X-Goog-FieldMask") != "photos" {
				t.Fatalf("unexpected field mask: %q", r.Header.Get("X-Goog-FieldMask"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"photos": []map[string]string{
					{"name": "places/test-place/photos/photo-token"},
				},
			})
		case "/v1/places/test-place/photos/photo-token/media":
			if r.Header.Get("X-Goog-Api-Key") != "places-key" {
				t.Fatalf("missing Places API key header")
			}
			if r.URL.Query().Get("maxWidthPx") != "800" {
				t.Fatalf("unexpected maxWidthPx: %q", r.URL.Query().Get("maxWidthPx"))
			}
			http.Redirect(w, r, imageServer.URL+"/photo.jpg", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	originalDetailsEndpoint := placeDetailsEndpoint
	originalMediaEndpoint := placePhotoMediaEndpoint
	placeDetailsEndpoint = upstream.URL + "/v1/places"
	placePhotoMediaEndpoint = upstream.URL + "/v1"
	t.Cleanup(func() {
		placeDetailsEndpoint = originalDetailsEndpoint
		placePhotoMediaEndpoint = originalMediaEndpoint
	})

	handler := NewPlacePhotoProxyHandler(&config.Config{
		MapsAPIKey:   "legacy-key",
		PlacesAPIKey: "places-key",
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/maps/place-photo?place_id=test-place&maxwidth=800",
		nil,
	)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("unexpected content type: %q", response.Header().Get("Content-Type"))
	}
	if response.Body.String() != "fake-jpeg" {
		t.Fatalf("unexpected image body: %q", response.Body.String())
	}
}

func TestPlacePhotoProxyReturnsNotFoundWhenPlaceHasNoPhotos(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"photos": []any{}})
	}))
	defer upstream.Close()

	originalDetailsEndpoint := placeDetailsEndpoint
	placeDetailsEndpoint = upstream.URL
	t.Cleanup(func() {
		placeDetailsEndpoint = originalDetailsEndpoint
	})

	handler := NewPlacePhotoProxyHandler(&config.Config{PlacesAPIKey: "places-key"})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/maps/place-photo?place_id=test-place",
		nil,
	)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNotFound)
	}
}
