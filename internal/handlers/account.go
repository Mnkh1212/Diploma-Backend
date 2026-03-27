package handlers

import (
	"net/http"

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
