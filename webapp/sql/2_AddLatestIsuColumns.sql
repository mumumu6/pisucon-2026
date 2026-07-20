ALTER TABLE `isu`
  ADD COLUMN `latest_timestamp` DATETIME,
  ADD COLUMN `latest_is_sitting` TINYINT(1),
  ADD COLUMN `latest_condition` VARCHAR(255),
  ADD COLUMN `latest_message` VARCHAR(255),
  ADD INDEX `idx_isu_character_latest_timestamp` (`character`, `latest_timestamp`);

UPDATE `isu` AS i
JOIN `isu_condition` AS ic
  ON ic.jia_isu_uuid = i.jia_isu_uuid
  AND ic.timestamp = (
    SELECT MAX(`timestamp`)
    FROM `isu_condition`
    WHERE jia_isu_uuid = i.jia_isu_uuid
  )
SET
  i.latest_timestamp = ic.timestamp,
  i.latest_is_sitting = ic.is_sitting,
  i.latest_condition = ic.condition,
  i.latest_message = ic.message;
