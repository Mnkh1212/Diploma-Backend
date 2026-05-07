package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AccountHandler struct {
	DB *gorm.DB
}

func NewAccountHandler(db *gorm.DB) *AccountHandler {
	return &AccountHandler{DB: db}
}

func (h *AccountHandler) Create(c *gin.Context) {
	userID := c.GetUint("user_id")

	var input struct {
		Name    string  `json:"name" binding:"required"`
		Type    string  `json:"type" binding:"required"`
		Balance float64 `json:"balance"`
		Icon    string  `json:"icon"`
		Color   string  `json:"color"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	account := models.Account{
		UserID:  userID,
		Name:    input.Name,
		Type:    input.Type,
		Balance: input.Balance,
		Icon:    input.Icon,
		Color:   input.Color,
	}

	if err := h.DB.Create(&account).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
		return
	}

	c.JSON(http.StatusCreated, account)
	LogActivity(h.DB, userID, "create_account", "account", account.ID, "", "success", c.ClientIP())
}

func (h *AccountHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")

	var accounts []models.Account
	h.DB.Where("user_id = ?", userID).Find(&accounts)

	c.JSON(http.StatusOK, accounts)
}

func (h *AccountHandler) Update(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var account models.Account
	if err := h.DB.Where("user_id = ?", userID).First(&account, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	var input struct {
		Name  string `json:"name"`
		Icon  string `json:"icon"`
		Color string `json:"color"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.Name != "" {
		account.Name = input.Name
	}
	if input.Icon != "" {
		account.Icon = input.Icon
	}
	if input.Color != "" {
		account.Color = input.Color
	}

	h.DB.Save(&account)
	c.JSON(http.StatusOK, account)
}

func (h *AccountHandler) Delete(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var account models.Account
	if err := h.DB.Where("user_id = ?", userID).First(&account, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	h.DB.Delete(&account)
	c.JSON(http.StatusOK, gin.H{"message": "Account deleted"})
	idUint, _ := strconv.ParseUint(id, 10, 32)
	LogActivity(h.DB, userID, "delete_account", "account", uint(idUint), "", "success", c.ClientIP())
}

func (h *AccountHandler) GetCategories(c *gin.Context) {
	catType := c.Query("type")

	var categories []models.Category
	query := h.DB
	if catType != "" {
		query = query.Where("type = ?", catType)
	}
	query.Find(&categories)

	c.JSON(http.StatusOK, categories)
}

// CreateCategory - хэрэглэгчийн гар оруулсан шинэ ангилал нэмэх.
// Ижил нэр + type-тай ангилал байгаа бол түүнийг буцаана (find-or-create).
func (h *AccountHandler) CreateCategory(c *gin.Context) {
	var req models.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Нэр шаардлагатай"})
		return
	}

	// Find-or-create
	var existing models.Category
	if err := h.DB.Where("LOWER(name) = LOWER(?) AND type = ?", name, req.Type).
		First(&existing).Error; err == nil {
		c.JSON(http.StatusOK, existing)
		return
	}

	icon := req.Icon
	if icon == "" {
		icon = "pricetag-outline"
	}
	color := req.Color
	if color == "" {
		color = pickCategoryColor(name)
	}

	cat := models.Category{
		Name:  name,
		Type:  req.Type,
		Icon:  icon,
		Color: color,
	}
	if err := h.DB.Create(&cat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ангилал үүсгэж чадсангүй"})
		return
	}
	c.JSON(http.StatusCreated, cat)
}

// pickCategoryColor - ангиллын нэрнээс тогтмол өнгө сонгоно.
func pickCategoryColor(seed string) string {
	palette := []string{
		"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4", "#FFEAA7",
		"#DDA0DD", "#F39C12", "#E74C3C", "#3498DB", "#9B59B6",
		"#E056A0", "#00B894", "#6C5CE7", "#FDCB6E", "#74B9FF", "#A29BFE",
	}
	var sum int
	for _, r := range seed {
		sum += int(r)
	}
	return palette[sum%len(palette)]
}
