package products

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Request / Response types ────────────────────────────────────────────────

type ProductInput struct {
	Name        string  `json:"name" binding:"required"`
	Price       float64 `json:"price"`
	Description string  `json:"description"`
	ImageURL    string  `json:"image_url"`    // external https:// URL
	ImageBase64 string  `json:"image_base64"` // data URL or raw base64 from file upload
	Link        string  `json:"link"`
}

type ProductResponse struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	Name        string    `json:"name"`
	Price       float64   `json:"price"`
	Description string    `json:"description"`
	ImageURL    string    `json:"image_url"`
	Link        string    `json:"link"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toResponse(p models.Product) ProductResponse {
	imgURL := p.ImageURL
	if len(p.ImageData) > 0 {
		imgURL = fmt.Sprintf("/api/v1/products/%s/image", p.ID)
	}
	return ProductResponse{
		ID:          p.ID,
		TenantID:    p.TenantID,
		Name:        p.Name,
		Price:       p.Price,
		Description: p.Description,
		ImageURL:    imgURL,
		Link:        p.Link,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// decodeMedia decodes a base64 string (raw or data-URL prefix) into bytes + MIME.
func decodeMedia(raw string) ([]byte, string, error) {
	mime := "image/jpeg"
	b64 := raw

	if idx := strings.Index(raw, ";base64,"); idx != -1 {
		mime = strings.TrimPrefix(raw[:idx], "data:")
		b64 = raw[idx+8:]
	}

	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(b64)
	}
	return data, mime, err
}

// ── Handlers ────────────────────────────────────────────────────────────────

func list(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var products []models.Product
	if err := database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&products).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	out := make([]ProductResponse, len(products))
	for i, p := range products {
		out[i] = toResponse(p)
	}
	c.JSON(http.StatusOK, out)
}

func create(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var input ProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	product := models.Product{
		TenantID:    tenantID,
		Name:        input.Name,
		Price:       input.Price,
		Description: input.Description,
		Link:        input.Link,
	}

	if input.ImageBase64 != "" {
		data, mime, err := decodeMedia(input.ImageBase64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image_base64"})
			return
		}
		if len(data) > 5*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image too large (max 5 MB)"})
			return
		}
		product.ImageData = data
		product.ImageMime = mime
	} else {
		product.ImageURL = input.ImageURL
	}

	if err := database.DB.Create(&product).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create product"})
		return
	}
	c.JSON(http.StatusCreated, toResponse(product))
}

func update(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	productID := c.Param("id")

	var product models.Product
	if err := database.DB.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}

	var input ProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	product.Name = input.Name
	product.Price = input.Price
	product.Description = input.Description
	product.Link = input.Link

	ownImagePath := fmt.Sprintf("/api/v1/products/%s/image", product.ID)

	switch {
	case input.ImageBase64 != "":
		// New file upload
		data, mime, err := decodeMedia(input.ImageBase64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image_base64"})
			return
		}
		if len(data) > 5*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image too large (max 5 MB)"})
			return
		}
		product.ImageData = data
		product.ImageMime = mime
		product.ImageURL = ""

	case input.ImageURL == ownImagePath:
		// Frontend sent back the same image path — user didn't change the image.
		// Preserve existing ImageData / ImageMime unchanged.

	case input.ImageURL != "":
		// External URL provided — switch to URL, clear any stored bytes.
		product.ImageURL = input.ImageURL
		product.ImageData = nil
		product.ImageMime = ""

	default:
		// Both empty → user removed the image.
		product.ImageURL = ""
		product.ImageData = nil
		product.ImageMime = ""
	}

	database.DB.Save(&product)
	c.JSON(http.StatusOK, toResponse(product))
}

func remove(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	productID := c.Param("id")

	var product models.Product
	if err := database.DB.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}

	database.DB.Delete(&product)
	c.JSON(http.StatusOK, gin.H{"message": "Product deleted"})
}

// getImage serves the stored binary image for a product.
// Auth is via ?token= query param (middleware.Auth handles this).
func getImage(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	productID := c.Param("id")

	var product models.Product
	if err := database.DB.Select("id, tenant_id, image_data, image_mime").
		Where("id = ? AND tenant_id = ?", productID, tenantID).
		First(&product).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}

	if len(product.ImageData) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no image"})
		return
	}

	mime := product.ImageMime
	if mime == "" {
		mime = "image/jpeg"
	}

	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, mime, product.ImageData)
}
