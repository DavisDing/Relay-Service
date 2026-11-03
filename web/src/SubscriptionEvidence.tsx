export function subscriptionCountKnown(count: number | undefined, state: string | undefined) {
  return state === 'available' && count !== undefined && Number.isSafeInteger(count) && count >= 0;
}
export function subscriptionCountLabel(count: number | undefined, state: string | undefined, unit = '') {
  return subscriptionCountKnown(count, state) ? `${count}${unit}` : '未知';
}
