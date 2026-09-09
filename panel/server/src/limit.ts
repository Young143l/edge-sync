/** 下载并发信号量：全局并发 max，超出排队；队列超过 maxQueue 时拒绝。 */
export class Limiter {
  private active = 0
  private readonly queue: (() => void)[] = []

  constructor(
    private readonly max: number,
    private readonly maxQueue = 8,
  ) {}

  async acquire(): Promise<void> {
    if (this.active + this.queue.length < this.max) {
      this.active++
      return
    }
    if (this.queue.length >= this.maxQueue) {
      throw new Error('too many concurrent downloads')
    }
    await new Promise<void>((r) => this.queue.push(r))
    this.active++
  }

  release(): void {
    this.active--
    const next = this.queue.shift()
    if (next) next()
  }

  get pending(): number {
    return this.queue.length
  }
}
