ALTER TABLE `isu`
  ADD COLUMN `latest_timestamp` DATETIME,
  ADD COLUMN `latest_is_sitting` TINYINT(1),
  ADD COLUMN `latest_condition` VARCHAR(255),
  ADD COLUMN `latest_message` VARCHAR(255);
