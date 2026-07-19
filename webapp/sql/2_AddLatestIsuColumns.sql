ALTER TABLE `isu`
  ADD COLUMN `latest_timestamp` DATETIME,
  ADD COLUMN `latest_is_sitting` TINYINT(1),
  ADD COLUMN `latest_condition` VARCHAR(255),
  ADD COLUMN `latest_message` VARCHAR(255);

UPDATE `isu` AS i
JOIN `isu_condition` AS ic
  ON ic.id = (
    SELECT id
    FROM `isu_condition`
    WHERE jia_isu_uuid = i.jia_isu_uuid
    ORDER BY `timestamp` DESC, id DESC
    LIMIT 1
  )
SET
  i.latest_timestamp = ic.timestamp,
  i.latest_is_sitting = ic.is_sitting,
  i.latest_condition = ic.condition,
  i.latest_message = ic.message;
