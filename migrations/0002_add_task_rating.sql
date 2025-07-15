-- Migration: Add task rating
-- Version: 0002
-- Created: 2025-01-15

-- Добавляем поле для голоса пользователя за свою задачу
ALTER TABLE tasks ADD COLUMN rating TEXT CHECK (rating IN ('upvote', 'downvote', NULL));

-- Добавляем индекс на rating для статистики
CREATE INDEX idx_tasks_rating ON tasks(rating);
