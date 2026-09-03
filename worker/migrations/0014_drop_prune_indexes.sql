-- ============================================================================
-- 0014_drop_prune_indexes
-- ============================================================================
-- Write-cost reduction. The two indexes dropped here exist only to speed the
-- list-hygiene prune query (idx_engagement_prunable_by_count /
-- idx_engagement_prunable_by_recency, from 0007). Their key columns —
-- messages_since_last_engagement and last_engagement_at_ms — change on nearly
-- every ingested event, so each event re-writes both index entries. While
-- auto-prune is disabled (LIST_HYGIENE_AUTO_PRUNE_ENABLED unset) and the
-- sunset automation is unbuilt, they are pure write amplification with no
-- read benefit: dropping them cuts contact_engagement writes roughly in half.
--
-- No data or query correctness changes — the manual prune surface still works,
-- it just scans contact_engagement for the target list instead of seeking.
-- RECREATE these before enabling auto-prune / the sunset automation:
--
--   CREATE INDEX idx_engagement_prunable_by_count
--     ON contact_engagement(list_id, messages_since_last_engagement DESC);
--   CREATE INDEX idx_engagement_prunable_by_recency
--     ON contact_engagement(list_id, last_engagement_at_ms ASC);
-- ============================================================================

DROP INDEX IF EXISTS idx_engagement_prunable_by_count;
DROP INDEX IF EXISTS idx_engagement_prunable_by_recency;
