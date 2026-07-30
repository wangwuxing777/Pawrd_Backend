package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
)

const (
	defaultPlacePhotoWidth = 800
	maxPlacePhotoWidth     = 1600
)

var placePhotoEndpoint = "https://maps.googleapis.com/maps/api/place/photo"
var placeDetailsEndpoint = "https://places.googleapis.com/v1/places"
var placePhotoMediaEndpoint = "https://places.googleapis.com/v1"

var errPlaceHasNoPhoto = errors.New("place has no photos")

func materializeClinicPhotoURL(r *http.Request, clinic *models.Clinic) {
	if clinic == nil {
		return
	}

	placeID := strings.TrimSpace(clinic.GooglePlaceID)
	photoReference := strings.TrimSpace(clinic.PhotoReference)
	if placeID == "" && photoReference == "" {
		if clinic != nil {
			clinic.PhotoURL = ""
		}
		return
	}

	query := url.Values{}
	if placeID != "" {
		query.Set("place_id", placeID)
	}
	if photoReference != "" {
		// Kept for compatibility with older deployments. New responses use
		// place_id to refresh an expiring Places API (New) photo resource.
		query.Set("reference", photoReference)
	}
	query.Set("maxwidth", strconv.Itoa(defaultPlacePhotoWidth))
	clinic.PhotoURL = requestBaseURL(r) + "/api/maps/place-photo?" + query.Encode()
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "https" {
		scheme = "https"
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func NewPlacePhotoProxyHandler(cfg *config.Config) http.HandlerFunc {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				// Place Photo media redirects to Google-hosted image storage.
				// Never forward the server-only Places API key to that host.
				req.Header.Del("X-Goog-Api-Key")
			}
			return nil
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		apiKey := configuredPlacesAPIKey(cfg)
		if apiKey == "" {
			http.Error(w, "Google Places is not configured", http.StatusServiceUnavailable)
			return
		}

		placeID := strings.TrimSpace(r.URL.Query().Get("place_id"))
		reference := strings.TrimSpace(r.URL.Query().Get("reference"))
		if placeID == "" && reference == "" {
			http.Error(w, "place_id or reference is required", http.StatusBadRequest)
			return
		}
		if placeID != "" && (len(placeID) > 256 || strings.ContainsAny(placeID, "/\r\n")) {
			http.Error(w, "invalid place_id", http.StatusBadRequest)
			return
		}
		if reference != "" && (len(reference) > 2048 || strings.ContainsAny(reference, "\r\n")) {
			http.Error(w, "invalid photo reference", http.StatusBadRequest)
			return
		}

		width := defaultPlacePhotoWidth
		if rawWidth := strings.TrimSpace(r.URL.Query().Get("maxwidth")); rawWidth != "" {
			parsed, err := strconv.Atoi(rawWidth)
			if err != nil || parsed < 1 || parsed > maxPlacePhotoWidth {
				http.Error(w, fmt.Sprintf("maxwidth must be between 1 and %d", maxPlacePhotoWidth), http.StatusBadRequest)
				return
			}
			width = parsed
		}

		var (
			upstreamResponse *http.Response
			err              error
		)
		if placeID != "" {
			upstreamResponse, err = fetchPlacePhotoNew(r, client, apiKey, placeID, width)
		} else {
			upstreamResponse, err = fetchPlacePhotoLegacy(client, apiKey, reference, width)
		}
		if err != nil {
			if errors.Is(err, errPlaceHasNoPhoto) {
				http.Error(w, "Google Place has no photo", http.StatusNotFound)
				return
			}
			if placeID != "" {
				// New API requests keep the key in a header, so this error is
				// safe to log and preserves the upstream status for diagnosis.
				fmt.Printf("Google Place Photo request failed for place_id=%q: %v\n", placeID, err)
			} else {
				// Legacy request URLs contain the API key as a query value.
				fmt.Println("Google Legacy Place Photo request failed")
			}
			http.Error(w, "Google Place Photos request failed", http.StatusBadGateway)
			return
		}
		defer upstreamResponse.Body.Close()

		if upstreamResponse.StatusCode != http.StatusOK {
			fmt.Printf(
				"Google Place Photo media returned status %d for place_id=%q\n",
				upstreamResponse.StatusCode,
				placeID,
			)
			http.Error(w, "Google Place Photos returned an error", http.StatusBadGateway)
			return
		}
		contentType := upstreamResponse.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			http.Error(w, "Google Place Photos returned non-image content", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, upstreamResponse.Body); err != nil {
			return
		}
	}
}

func configuredPlacesAPIKey(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if key := strings.TrimSpace(cfg.PlacesAPIKey); key != "" {
		return key
	}
	return strings.TrimSpace(cfg.MapsAPIKey)
}

func fetchPlacePhotoNew(
	r *http.Request,
	client *http.Client,
	apiKey string,
	placeID string,
	width int,
) (*http.Response, error) {
	detailsURL := strings.TrimRight(placeDetailsEndpoint, "/") + "/" + url.PathEscape(placeID)
	detailsRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, detailsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create Place Details request: %w", err)
	}
	detailsRequest.Header.Set("X-Goog-Api-Key", apiKey)
	detailsRequest.Header.Set("X-Goog-FieldMask", "photos")

	detailsResponse, err := client.Do(detailsRequest)
	if err != nil {
		return nil, fmt.Errorf("Place Details request: %w", err)
	}
	defer detailsResponse.Body.Close()
	if detailsResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Place Details returned status %d", detailsResponse.StatusCode)
	}

	var details struct {
		Photos []struct {
			Name string `json:"name"`
		} `json:"photos"`
	}
	if err := json.NewDecoder(io.LimitReader(detailsResponse.Body, 1<<20)).Decode(&details); err != nil {
		return nil, fmt.Errorf("decode Place Details response: %w", err)
	}
	if len(details.Photos) == 0 || strings.TrimSpace(details.Photos[0].Name) == "" {
		return nil, errPlaceHasNoPhoto
	}

	photoName, err := validatedPhotoResourceName(details.Photos[0].Name, placeID)
	if err != nil {
		return nil, err
	}
	mediaURL := strings.TrimRight(placePhotoMediaEndpoint, "/") + "/" + photoName + "/media"
	parsedMediaURL, err := url.Parse(mediaURL)
	if err != nil {
		return nil, fmt.Errorf("create Place Photo media URL: %w", err)
	}
	query := parsedMediaURL.Query()
	query.Set("maxWidthPx", strconv.Itoa(width))
	parsedMediaURL.RawQuery = query.Encode()

	mediaRequest, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodGet,
		parsedMediaURL.String(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Place Photo media request: %w", err)
	}
	mediaRequest.Header.Set("X-Goog-Api-Key", apiKey)
	return client.Do(mediaRequest)
}

func validatedPhotoResourceName(rawName string, placeID string) (string, error) {
	parts := strings.Split(strings.TrimSpace(rawName), "/")
	if len(parts) != 4 ||
		parts[0] != "places" ||
		parts[1] != placeID ||
		parts[2] != "photos" ||
		parts[3] == "" ||
		strings.ContainsAny(parts[3], "\r\n") {
		return "", errors.New("Place Details returned an invalid photo resource")
	}
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/"), nil
}

func fetchPlacePhotoLegacy(
	client *http.Client,
	apiKey string,
	reference string,
	width int,
) (*http.Response, error) {
	query := url.Values{}
	query.Set("photo_reference", reference)
	query.Set("maxwidth", strconv.Itoa(width))
	query.Set("key", apiKey)
	return client.Get(placePhotoEndpoint + "?" + query.Encode())
}
