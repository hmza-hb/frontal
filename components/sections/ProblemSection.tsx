"use client";

import { motion } from "framer-motion";
import { Lock, EyeOff, Unplug, AlertCircle } from "lucide-react";

export default function ProblemSection() {
  const cards = [
    {
      num: "01",
      icon: Lock,
      title: "Limited Access",
      description:
        "Most people can enjoy entertainment, but only a select few can participate in funding it.",
      badge: "High Barrier",
    },
    {
      num: "02",
      icon: EyeOff,
      title: "Broken Discovery",
      description:
        "Exceptional stories often struggle to find the capital and audience they deserve.",
      badge: "Market Bottleneck",
    },
    {
      num: "03",
      icon: Unplug,
      title: "Missed Opportunity",
      description:
        "Creators, investors, and audiences remain disconnected from each other.",
      badge: "Siloed Network",
    },
  ];

  return (
    <section id="problem" className="py-24 md:py-32 bg-slate-100/70 border-b border-slate-200/60 relative">
      <div className="max-w-6xl mx-auto px-4 sm:px-6">
        
        {/* Section Header */}
        <div className="max-w-3xl space-y-4 mb-16">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-md bg-rose-100 text-rose-800 font-mono text-xs uppercase tracking-wider">
            <AlertCircle className="w-3.5 h-3.5" />
            <span>— SECTION 2: THE PROBLEM</span>
          </div>
          
          <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 leading-tight">
            Great Stories Shouldn't Depend on a Few Gatekeepers.
          </h2>

          <p className="text-lg text-slate-600 font-normal leading-relaxed">
            For decades, entertainment financing has been controlled by a small network of studios, investors, and industry insiders. This limits which stories get made, who can participate, and how audiences connect with the films they love.
          </p>
        </div>

        {/* Three Problem Cards Grid */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 lg:gap-8">
          {cards.map((card, idx) => {
            const Icon = card.icon;
            return (
              <motion.div
                key={card.title}
                initial={{ opacity: 0, y: 20 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true }}
                transition={{ duration: 0.5, delay: idx * 0.15 }}
                className="group relative bg-white rounded-2xl p-8 border border-slate-200/80 shadow-sm hover:shadow-xl transition-all duration-300 hover:-translate-y-1 flex flex-col justify-between"
              >
                <div>
                  {/* Top Bar inside Card */}
                  <div className="flex items-center justify-between mb-8">
                    <span className="font-mono text-2xl font-bold text-slate-300 group-hover:text-slate-900 transition-colors">
                      {card.num}
                    </span>
                    <span className="text-[11px] font-mono px-2.5 py-1 rounded bg-slate-100 text-slate-600 uppercase tracking-wider">
                      {card.badge}
                    </span>
                  </div>

                  {/* Icon */}
                  <div className="w-12 h-12 rounded-xl bg-slate-100 flex items-center justify-center text-slate-900 mb-6 group-hover:bg-slate-900 group-hover:text-white transition-all duration-300">
                    <Icon className="w-6 h-6" />
                  </div>

                  {/* Title */}
                  <h3 className="text-xl font-bold text-slate-900 mb-3 group-hover:text-slate-900">
                    {card.title}
                  </h3>

                  {/* Description */}
                  <p className="text-sm text-slate-600 leading-relaxed">
                    {card.description}
                  </p>
                </div>

                {/* Bottom Border Highlight on Hover */}
                <div className="mt-8 pt-4 border-t border-slate-100 text-xs font-mono text-slate-400 group-hover:text-rose-600 flex items-center justify-between transition-colors">
                  <span>SYSTEMIC LIMITATION</span>
                  <span>[ANALYSIS]</span>
                </div>
              </motion.div>
            );
          })}
        </div>

      </div>
    </section>
  );
}
