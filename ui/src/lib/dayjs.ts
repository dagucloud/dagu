import dayjs from 'dayjs';
import advancedFormat from 'dayjs/plugin/advancedFormat';
import customParseFormat from 'dayjs/plugin/customParseFormat';
import duration from 'dayjs/plugin/duration';
import relativeTime from 'dayjs/plugin/relativeTime';
import timezone from 'dayjs/plugin/timezone';
import utc from 'dayjs/plugin/utc';
import 'dayjs/locale/ja';
import 'dayjs/locale/zh-cn';

// Extend dayjs with plugins
dayjs.extend(utc);
dayjs.extend(timezone);
dayjs.extend(duration);
dayjs.extend(customParseFormat);
dayjs.extend(relativeTime);
dayjs.extend(advancedFormat);

// Custom duration format function to mimic moment-duration-format
// This adds a format method to the duration object
const oldDurationPrototype = dayjs.duration.prototype;
const newDurationPrototype = Object.create(oldDurationPrototype);

newDurationPrototype.format = function (formatStr: string) {
  const days = Math.floor(this.asDays());
  const hours = this.hours();
  const minutes = this.minutes();
  const seconds = this.seconds();

  let result = formatStr;

  // Replace tokens with values
  if (result.includes('d')) {
    result = result.replace(/d+/g, days.toString());
  }

  if (result.includes('h')) {
    result = result.replace(/h+/g, hours.toString());
  }

  if (result.includes('m')) {
    result = result.replace(/m+/g, minutes.toString());
  }

  if (result.includes('s')) {
    result = result.replace(/s+/g, seconds.toString());
  }

  // Remove square brackets that moment-duration-format uses
  result = result.replace(/\[([^\]]+)\]/g, '$1');

  return result;
};

// Apply the new prototype
dayjs.duration.prototype = newDurationPrototype;

export default dayjs;
