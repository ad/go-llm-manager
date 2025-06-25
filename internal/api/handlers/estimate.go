package handlers

import (
	"fmt"
	"math"
	"time"

	"github.com/ad/go-llm-manager/internal/database"
)

// calculateEstimatedWaitTime calculates wait time for new tasks
func calculateEstimatedWaitTime(db *database.DB) (string, error) {
	now := time.Now().UnixMilli()

	// Get active processors and their metrics
	processorsQuery := `
		SELECT 
			pm.processor_id,
			COALESCE(pm.cpu_usage, 0) as cpu_usage,
			COALESCE(pm.memory_usage, 0) as memory_usage,
			COALESCE(pm.queue_size, 0) as queue_size,
			pm.last_updated,
			COUNT(t.id) as active_tasks
		FROM processor_metrics pm
		LEFT JOIN tasks t ON pm.processor_id = t.processor_id AND t.status = 'processing'
		WHERE pm.last_updated > ? - 300000
		GROUP BY pm.processor_id
	`

	rows, err := db.Query(processorsQuery, now)
	if err != nil {
		return "Unable to estimate", err
	}
	defer rows.Close()

	var activeProcessors []map[string]interface{}
	for rows.Next() {
		var processorID string
		var cpuUsage, memoryUsage, queueSize float64
		var lastUpdated int64
		var activeTasks int

		err := rows.Scan(&processorID, &cpuUsage, &memoryUsage, &queueSize, &lastUpdated, &activeTasks)
		if err != nil {
			continue
		}

		activeProcessors = append(activeProcessors, map[string]interface{}{
			"processor_id": processorID,
			"cpu_usage":    cpuUsage,
			"memory_usage": memoryUsage,
			"queue_size":   queueSize,
			"active_tasks": activeTasks,
		})
	}

	// Get pending tasks count
	var pendingTasksCount int
	err = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'pending'").Scan(&pendingTasksCount)
	if err != nil {
		pendingTasksCount = 0
	}

	// Get average processing time from completed tasks (last 24 hours)
	avgTimeQuery := `
		SELECT 
			COALESCE(AVG(completed_at - processing_started_at), 45000) as avg_processing_time,
			COUNT(*) as completed_count
		FROM tasks 
		WHERE status = 'completed' 
			AND completed_at > ? - 86400000
			AND processing_started_at IS NOT NULL
			AND completed_at IS NOT NULL
	`

	var avgProcessingTime float64
	var completedCount int
	err = db.QueryRow(avgTimeQuery, now).Scan(&avgProcessingTime, &completedCount)
	if err != nil {
		avgProcessingTime = 45000 // Default 45 seconds
	}

	// If no active processors, return high estimate
	if len(activeProcessors) == 0 {
		return "10-15 minutes (no active processors)", nil
	}

	// Calculate total processing capacity
	totalCapacity := 0.0
	for _, processor := range activeProcessors {
		cpuUsage := processor["cpu_usage"].(float64)
		memoryUsage := processor["memory_usage"].(float64)
		activeTasks := float64(processor["active_tasks"].(int))

		loadFactor := (cpuUsage*0.3 + memoryUsage*0.3 + activeTasks*0.4) / 100
		capacityFactor := math.Max(0.1, 1-loadFactor) // Minimum 10% capacity
		totalCapacity += capacityFactor
	}

	// Calculate queue position (assuming fair distribution)
	queuePosition := math.Ceil(float64(pendingTasksCount) / math.Max(1, totalCapacity))

	// Calculate estimated wait time
	estimatedWaitMs := (queuePosition * avgProcessingTime) + (avgProcessingTime * 0.5) // Add buffer

	// Convert to human-readable format
	if estimatedWaitMs < 10000 {
		return "< 10 секунд", nil
	} else if estimatedWaitMs < 30000 {
		return "< 30 секунд", nil
	} else if estimatedWaitMs < 60000 {
		return "< 1 минуты", nil
	}
	waitTimeMinutes := math.Ceil(estimatedWaitMs / 60000)

	switch {
	case waitTimeMinutes < 1:
		return "< 1 минуты", nil
	case waitTimeMinutes <= 2:
		return "1-2 минуты", nil
	case waitTimeMinutes <= 5:
		return "2-5 минут", nil
	case waitTimeMinutes <= 10:
		return "5-10 минут", nil
	case waitTimeMinutes <= 15:
		return "10-15 минут", nil
	default:
		return fmt.Sprintf("%.0f минут", waitTimeMinutes), nil
	}
}
