package handlers

import (
	"net/http"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ScheduledPaymentHandler struct {
	DB *gorm.DB
}

func NewScheduledPaymentHandler(db *gorm.DB) *ScheduledPaymentHandler {
	return &ScheduledPaymentHandler{DB: db}
}

func (h *ScheduledPaymentHandler) Create(c *gin.Context) {
	userID := c.GetUint("user_id")

	var input struct {
		CategoryID  uint    `json:"category_id" binding:"required"`
		AccountID   uint    `json:"account_id" binding:"required"`
		Amount      float64 `json:"amount" binding:"required"`
		Description string  `json:"description" binding:"required"`
		Frequency   string  `json:"frequency" binding:"required"`
		NextDate    string  `json:"next_date" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nextDate, err := time.Parse("2006-01-02", input.NextDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format"})
		return
	}

	payment := models.ScheduledPayment{
		UserID:      userID,
		CategoryID:  input.CategoryID,
		AccountID:   input.AccountID,
		Amount:      input.Amount,
		Description: input.Description,
		Frequency:   input.Frequency,
		NextDate:    nextDate,
		IsActive:    true,
	}

	if err := h.DB.Create(&payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create scheduled payment"})
		return
	}

	h.DB.Preload("Category").First(&payment, payment.ID)
	c.JSON(http.StatusCreated, payment)
}

func (h *ScheduledPaymentHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")

	var payments []models.ScheduledPayment
	h.DB.Preload("Category").
		Where("user_id = ?", userID).
		Order("next_date ASC").
		Find(&payments)

	c.JSON(http.StatusOK, payments)
}

func (h *ScheduledPaymentHandler) Delete(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var payment models.ScheduledPayment
	if err := h.DB.Where("user_id = ?", userID).First(&payment, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scheduled payment not found"})
		return
	}

	h.DB.Delete(&payment)
	c.JSON(http.StatusOK, gin.H{"message": "Scheduled payment deleted"})
}

func (h *ScheduledPaymentHandler) Toggle(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var payment models.ScheduledPayment
	if err := h.DB.Where("user_id = ?", userID).First(&payment, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scheduled payment not found"})
		return
	}

	payment.IsActive = !payment.IsActive
	h.DB.Save(&payment)

	c.JSON(http.StatusOK, payment)
}
