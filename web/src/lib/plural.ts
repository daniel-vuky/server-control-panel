/** plural is "<n> <one>" for 1 and "<n> <many>" otherwise. */
export const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`
