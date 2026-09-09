import { useState, useEffect, useCallback, useMemo } from "react"
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { pb, isReadOnlyUser } from "@/lib/api"
import { formatBytes } from "@/lib/utils"
import type { DiskUsageReport, DiskCategoryItem } from "@/types"
import {
	HardDrive,
	RefreshCw,
	Copy,
	Check,
	AlertTriangle,
	FolderGit2,
	Package,
	Cpu,
	Layers,
	FileText,
	Trash2,
	Terminal,
	Sparkles,
	CheckCircle2,
} from "lucide-react"
import { PieChart, Pie, Cell, ResponsiveContainer, Tooltip as ChartTooltip } from "recharts"

const CATEGORY_COLORS: Record<string, string> = {
	Workspaces: "#3b82f6", // Blue
	"AI & Agent State": "#a855f7", // Purple
	"Package Caches": "#f59e0b", // Amber
	"User Cache": "#eab308", // Yellow
	"Docker & Containers": "#06b6d4", // Cyan
	"System Logs": "#f43f5e", // Rose
	"Snaps & Packages": "#6366f1", // Indigo
	"Temporary Files": "#ef4444", // Red
	"System Binaries": "#64748b", // Slate
	Other: "#94a3b8", // Muted Slate
	Free: "#10b981", // Emerald Green
}

function getCategoryIcon(cat: string) {
	switch (cat) {
		case "Workspaces":
			return <FolderGit2 className="size-3.5 text-blue-500" />
		case "AI & Agent State":
			return <Sparkles className="size-3.5 text-purple-500" />
		case "Package Caches":
		case "User Cache":
			return <Package className="size-3.5 text-amber-500" />
		case "Docker & Containers":
			return <Layers className="size-3.5 text-cyan-500" />
		case "System Logs":
			return <FileText className="size-3.5 text-rose-500" />
		default:
			return <HardDrive className="size-3.5 text-muted-foreground" />
	}
}

export function DiskUsageBreakdown({ systemId }: { systemId: string }) {
	const [report, setReport] = useState<DiskUsageReport | null>(null)
	const [loading, setLoading] = useState(false)
	const [copiedCmd, setCopiedCmd] = useState<string | null>(null)
	const [error, setError] = useState<string | null>(null)

	const fetchDiskUsage = useCallback(
		async (refresh = false) => {
			if (!systemId) return
			setLoading(true)
			setError(null)
			try {
				const endpoint = refresh ? "/api/beszel/disk-usage/refresh" : "/api/beszel/disk-usage"
				const method = refresh ? "POST" : "GET"
				const res = await pb.send<DiskUsageReport>(endpoint, {
					method,
					query: { system: systemId },
				})
				if (res && res.categories) {
					setReport(res)
				}
			} catch (err: any) {
				console.error("Failed to fetch disk categorization:", err)
				setError(err?.message || "Failed to scan disk space")
			} finally {
				setLoading(false)
			}
		},
		[systemId]
	)

	useEffect(() => {
		fetchDiskUsage(false)
	}, [fetchDiskUsage])

	const copyToClipboard = (cmd: string) => {
		navigator.clipboard.writeText(cmd)
		setCopiedCmd(cmd)
		setTimeout(() => setCopiedCmd(null), 2000)
	}

	const chartData = useMemo(() => {
		if (!report) return []
		const items: { name: string; value: number; color: string; human: string }[] = []

		// Group categories for cleaner chart
		const catTotals: Record<string, number> = {}
		for (const cat of report.categories) {
			catTotals[cat.category] = (catTotals[cat.category] || 0) + cat.size
		}

		for (const [catName, sizeBytes] of Object.entries(catTotals)) {
			const sizeGB = Number((sizeBytes / (1024 * 1024 * 1024)).toFixed(2))
			items.push({
				name: catName,
				value: sizeGB,
				color: CATEGORY_COLORS[catName] || "#94a3b8",
				human: `${sizeGB} GB`,
			})
		}

		if (report.freeBytes > 0) {
			const freeGB = Number((report.freeBytes / (1024 * 1024 * 1024)).toFixed(2))
			items.push({
				name: "Free Space",
				value: freeGB,
				color: CATEGORY_COLORS.Free,
				human: `${freeGB} GB`,
			})
		}

		return items
	}, [report])

	const bloatCount = useMemo(() => {
		if (!report?.categories) return 0
		return report.categories.filter((c) => c.status === "bloated").length
	}, [report])

	const reclaimableSize = useMemo(() => {
		if (!report?.categories) return ""
		const bytes = report.categories
			.filter((c) => (c.status === "bloated" || c.status === "warning") && c.cleanupCmd)
			.reduce((acc, c) => acc + c.size, 0)
		if (bytes === 0) return ""
		const gb = (bytes / (1024 * 1024 * 1024)).toFixed(1)
		return `~${gb} GB`
	}, [report])

	return (
		<Card className="col-span-full shadow-xs">
			<CardHeader className="flex flex-row items-center justify-between pb-3 gap-4">
				<div>
					<div className="flex items-center gap-2">
						<HardDrive className="size-5 text-primary" />
						<CardTitle className="text-lg">Disk Space Categorization & Bloat Diagnostics</CardTitle>
						{bloatCount > 0 && (
							<Badge variant="destructive" className="gap-1 px-2 py-0.5 text-xs font-semibold">
								<AlertTriangle className="size-3" />
								{bloatCount} Bloated Areas
							</Badge>
						)}
					</div>
					<CardDescription className="mt-1">
						Live disk usage categorization by workspaces, toolchains, caches, and system files with 1-click cleanup commands.
					</CardDescription>
				</div>
				<div className="flex items-center gap-2">
					<Button
						variant="outline"
						size="sm"
						onClick={() => fetchDiskUsage(true)}
						disabled={loading || isReadOnlyUser()}
						className="gap-1.5"
					>
						<RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />
						<span>{loading ? "Scanning..." : "Rescan Disk"}</span>
					</Button>
				</div>
			</CardHeader>

			<CardContent className="space-y-6">
				{/* Top Metrics Cards */}
				{report && (
					<div className="grid grid-cols-2 md:grid-cols-4 gap-3">
						<div className="p-3 bg-muted/40 rounded-lg border">
							<div className="text-xs text-muted-foreground font-medium">Total Disk Size</div>
							<div className="text-xl font-bold mt-1">
								{(report.totalBytes / (1024 * 1024 * 1024)).toFixed(1)} GB
							</div>
							<div className="text-xs text-muted-foreground">Mount: {report.rootMount}</div>
						</div>

						<div className="p-3 bg-muted/40 rounded-lg border">
							<div className="text-xs text-muted-foreground font-medium">Used Space</div>
							<div className="text-xl font-bold mt-1 text-amber-500">
								{(report.usedBytes / (1024 * 1024 * 1024)).toFixed(1)} GB
							</div>
							<div className="text-xs text-muted-foreground">{report.usedPercent.toFixed(1)}% capacity</div>
						</div>

						<div className="p-3 bg-muted/40 rounded-lg border">
							<div className="text-xs text-muted-foreground font-medium">Free Space</div>
							<div className="text-xl font-bold mt-1 text-emerald-500">
								{(report.freeBytes / (1024 * 1024 * 1024)).toFixed(1)} GB
							</div>
							<div className="text-xs text-muted-foreground">Available for storage</div>
						</div>

						<div className="p-3 bg-muted/40 rounded-lg border">
							<div className="text-xs text-muted-foreground font-medium">Reclaimable Bloat</div>
							<div className="text-xl font-bold mt-1 text-rose-500">{reclaimableSize || "0 GB"}</div>
							<div className="text-xs text-muted-foreground">From cleanup actions</div>
						</div>
					</div>
				)}

				{/* Visual Chart & Summary */}
				{report && chartData.length > 0 && (
					<div className="flex flex-col lg:flex-row items-center justify-between gap-6 p-4 rounded-xl bg-card border">
						{/* Donut Chart */}
						<div className="relative w-full lg:w-72 h-52 flex items-center justify-center">
							<ResponsiveContainer width="100%" height="100%">
								<PieChart>
									<Pie
										data={chartData}
										cx="50%"
										cy="50%"
										innerRadius={55}
										outerRadius={80}
										paddingAngle={3}
										dataKey="value"
									>
										{chartData.map((entry, index) => (
											<Cell key={`cell-${index}`} fill={entry.color} />
										))}
									</Pie>
									<ChartTooltip
										formatter={(val: any, name: any) => [`${val} GB`, name]}
										contentStyle={{ borderRadius: "8px", fontSize: "12px" }}
									/>
								</PieChart>
							</ResponsiveContainer>
							<div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
								<span className="text-xl font-bold">{report.usedPercent.toFixed(0)}%</span>
								<span className="text-[11px] text-muted-foreground uppercase">Used</span>
							</div>
						</div>

						{/* Legend Badges */}
						<div className="flex-1 grid grid-cols-2 sm:grid-cols-3 gap-2 text-xs">
							{chartData.map((item) => (
								<div key={item.name} className="flex items-center gap-2 p-2 rounded-md bg-muted/30 border">
									<div className="size-3 rounded-full shrink-0" style={{ backgroundColor: item.color }} />
									<div className="truncate">
										<div className="font-medium truncate">{item.name}</div>
										<div className="text-muted-foreground text-[11px]">{item.human}</div>
									</div>
								</div>
							))}
						</div>
					</div>
				)}

				{/* Breakdown Table */}
				<div className="rounded-md border overflow-hidden">
					<Table>
						<TableHeader className="bg-muted/50">
							<TableRow>
								<TableHead className="w-56">Category / Target</TableHead>
								<TableHead>Path</TableHead>
								<TableHead className="w-24 text-right">Size</TableHead>
								<TableHead className="w-28 text-right">% Disk</TableHead>
								<TableHead className="w-28">Status</TableHead>
								<TableHead className="min-w-64">Recommended Cleanup</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{loading && !report ? (
								<TableRow>
									<TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
										<div className="flex items-center justify-center gap-2">
											<RefreshCw className="size-4 animate-spin" />
											Scanning disk usage and categorizing paths...
										</div>
									</TableCell>
								</TableRow>
							) : report?.categories && report.categories.length > 0 ? (
								report.categories.map((item, idx) => (
									<TableRow key={`${item.path}-${idx}`} className="hover:bg-muted/30">
										<TableCell className="font-medium">
											<div className="flex items-center gap-2">
												{getCategoryIcon(item.category)}
												<span className="truncate">{item.name}</span>
											</div>
										</TableCell>

										<TableCell>
											<TooltipProvider>
												<Tooltip>
													<TooltipTrigger asChild>
														<span className="font-mono text-xs bg-muted/60 px-1.5 py-0.5 rounded cursor-default truncate max-w-xs block">
															{item.path}
														</span>
													</TooltipTrigger>
													<TooltipContent side="top">
														<p className="font-mono text-xs">{item.path}</p>
														{item.description && (
															<p className="text-xs text-muted-foreground mt-1">{item.description}</p>
														)}
													</TooltipContent>
												</Tooltip>
											</TooltipProvider>
										</TableCell>

										<TableCell className="text-right font-semibold">{item.size > (report.totalBytes || Infinity) ? formatBytes(Math.min(item.size, report.usedBytes)) : item.sizeHuman}</TableCell>

										<TableCell className="text-right">
											<div className="flex items-center justify-end gap-2">
												<span className="text-xs text-muted-foreground">{Math.min(item.percentDisk, 100).toFixed(1)}%</span>
												<div className="w-12 h-1.5 bg-muted rounded-full overflow-hidden">
													<div
														className="h-full rounded-full"
														style={{
															width: `${Math.min(item.percentDisk * 3, 100)}%`,
															backgroundColor:
																item.status === "bloated"
																	? "#f43f5e"
																	: item.status === "warning"
																	? "#f59e0b"
																	: "#10b981",
														}}
													/>
												</div>
											</div>
										</TableCell>

										<TableCell>
											{item.status === "bloated" ? (
												<Badge variant="destructive" className="gap-1 text-[11px] px-2 py-0">
													<AlertTriangle className="size-3" />
													Bloated
												</Badge>
											) : item.status === "warning" ? (
												<Badge
													variant="outline"
													className="gap-1 text-[11px] px-2 py-0 border-amber-500/50 text-amber-500 bg-amber-500/10"
												>
													Warning
												</Badge>
											) : (
												<Badge variant="secondary" className="gap-1 text-[11px] px-2 py-0">
													<CheckCircle2 className="size-3 text-emerald-500" />
													Clean
												</Badge>
											)}
										</TableCell>

										<TableCell>
											{item.cleanupCmd ? (
												<div className="flex items-center gap-2">
													<code className="text-xs font-mono bg-muted/80 px-2 py-1 rounded border max-w-sm truncate flex-1 select-all">
														{item.cleanupCmd}
													</code>
													<Button
														variant="ghost"
														size="icon"
														className="size-7 shrink-0 text-muted-foreground hover:text-foreground"
														onClick={() => copyToClipboard(item.cleanupCmd)}
														title="Copy cleanup command"
													>
														{copiedCmd === item.cleanupCmd ? (
															<Check className="size-3.5 text-emerald-500" />
														) : (
															<Copy className="size-3.5" />
														)}
													</Button>
												</div>
											) : (
												<span className="text-xs text-muted-foreground italic">No action needed</span>
											)}
										</TableCell>
									</TableRow>
								))
							) : (
								<TableRow>
									<TableCell colSpan={6} className="h-24 text-center text-muted-foreground">
										{error ? (
											<span className="text-destructive font-medium">{error}</span>
										) : (
											"No disk breakdown data available. Click 'Rescan Disk' to begin."
										)}
									</TableCell>
								</TableRow>
							)}
						</TableBody>
					</Table>
				</div>
			</CardContent>
		</Card>
	)
}

export default DiskUsageBreakdown

