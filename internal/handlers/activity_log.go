package handlers

import (
	"net/http"
	"strconv"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ActivityLogHandler struct {
	db *gorm.DB
}

func NewActivityLogHandler(db *gorm.DB) *ActivityLogHandler {
	return &ActivityLogHandler{db: db}
}

// LogActivity is a helper function other handlers can use
func LogActivity(db *gorm.DB, userID uint, action, entity string, entityID uint, details, status, ip string) {
	log := models.ActivityLog{
		UserID:    userID,
		Action:    action,
		Entity:    entity,
		EntityID:  entityID,
		Details:   details,
		Status:    status,
		IPAddress: ip,
	}
	db.Create(&log)
}

func (h *ActivityLogHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset := (page - 1) * limit

	var logs []models.ActivityLog
	var total int64

	query := h.db.Where("user_id = ?", userID)

	// Optional filters
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if entity := c.Query("entity"); entity != "" {
		query = query.Where("entity = ?", entity)
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	query.Model(&models.ActivityLog{}).Count(&total)
	query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs)

	c.JSON(http.StatusOK, models.ActivityLogResponse{
		Logs:  logs,
		Total: total,
	})
}

func (h *ActivityLogHandler) Summary(c *gin.Context) {
	userID := c.GetUint("user_id")

	type ActionCount struct {
		Action string `json:"action"`
		Count  int64  `json:"count"`
	}

	var counts []ActionCount
	h.db.Model(&models.ActivityLog{}).
		Select("action, COUNT(*) as count").
		Where("user_id = ?", userID).
		Group("action").
		Order("count DESC").
		Find(&counts)

	var total int64
	h.db.Model(&models.ActivityLog{}).Where("user_id = ?", userID).Count(&total)

	c.JSON(http.StatusOK, gin.H{
		"total":   total,
		"actions": counts,
	})
}
